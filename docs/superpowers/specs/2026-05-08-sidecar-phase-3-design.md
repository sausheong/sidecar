# Sidecar Phase 3 — Persistent Memory Design Spec

**Date:** 2026-05-08
**Status:** Approved
**Builds on:** Phase 1 (core runtime), Phase 2 (reactive runtime — complete)

---

## 1. Overview

Phase 3 gives Sidecar a persistent memory of each codebase it manages. After every task, a reflect step extracts architectural insights, workflow learnings, and policy constraints and stores them as vector-indexed memory entries. Before each coding agent run, the most relevant entries are retrieved via semantic search and injected into the agent's context.

The result: Sidecar gets progressively smarter about a codebase the longer it runs.

**Four deliverables:**
1. pgvector schema (`memory_entries`, `policies` tables)
2. Pluggable `EmbeddingProvider` interface with OpenAI default
3. Semantic retrieval at plan time (system prompt augmentation)
4. Reflect step (async Haiku call after each task output)

---

## 2. New Components

```
internal/
  memory/
    memory.go        EmbeddingProvider interface + package-level types
    openai.go        OpenAI text-embedding-3-small implementation (default)
    voyage.go        Voyage AI voyage-4 implementation (opt-in)
    reflect.go       Reflect step — Haiku extracts insights after each task
    retrieve.go      Semantic retrieval — cosine similarity via pgvector
    memory_test.go   Unit tests for pure functions
```

**Modified files:**
- `internal/store/schema.sql` — add `memory_entries` + `policies` tables + pgvector extension
- `internal/store/memory.go` — new file: `StoreMemory`, `SearchMemory`, `GetPolicies`, `StorePolicy` methods on `DB`
- `internal/loop/loop.go` — wire retrieval before coding agent, async reflect goroutine after output
- `internal/config/config.go` — add `EmbeddingConfig` section to `Config`

---

## 3. Schema

```sql
-- Requires pgvector PostgreSQL extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Workspace memory entries — embedded and retrieved by semantic similarity
CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    category     TEXT NOT NULL,   -- "episodic" | "semantic" | "procedural"
    content      TEXT NOT NULL,
    embedding    vector(1024),    -- 1024 dims: OpenAI (with dimensions param) + Voyage AI native
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- IVFFlat index for approximate nearest-neighbor search
CREATE INDEX IF NOT EXISTS memory_entries_embedding_idx
    ON memory_entries USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- Policy rules — loaded as a full list, no embedding needed
CREATE TABLE IF NOT EXISTS policies (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    rule         TEXT NOT NULL,
    source       TEXT NOT NULL DEFAULT 'yaml',  -- "yaml" | "learned"
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

**Note on dimensions:** The schema uses `vector(1024)`. OpenAI embeddings are requested with `dimensions: 1024`. Voyage AI `voyage-4` natively defaults to 1024 dimensions — no `output_dimension` override needed.

---

## 4. EmbeddingProvider Interface

```go
// internal/memory/memory.go

// EmbeddingProvider converts text to vector embeddings.
type EmbeddingProvider interface {
    // Embed converts texts to embeddings.
    // inputType is "document" (when storing) or "query" (when searching).
    Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error)
    // Dims returns the vector dimension this provider produces.
    Dims() int
}

// MemoryEntry is a single retrieved memory record.
type MemoryEntry struct {
    Category  string
    Content   string
    Score     float64 // cosine similarity (0-1)
}
```

### OpenAI implementation (`internal/memory/openai.go`)

- API: `POST https://api.openai.com/v1/embeddings`
- Model: `text-embedding-3-small` (default), configurable
- Auth: `Authorization: Bearer $OPENAI_API_KEY`
- Dims: 1024 (requested via `dimensions: 1024` parameter)
- `inputType` parameter is ignored (OpenAI does not use query/document distinction)

### Voyage AI implementation (`internal/memory/voyage.go`)

- API: `POST https://api.voyageai.com/v1/embeddings`
- Model: `voyage-4` (default), configurable
- Auth: `Authorization: Bearer $VOYAGE_API_KEY`
- Dims: 1024 (voyage-4 native default — no `output_dimension` override needed)
- `inputType` maps to `input_type: "document"` or `input_type: "query"` per Voyage API spec

---

## 5. sidecar.yaml Config Addition

```yaml
embedding:
  provider: openai                   # "openai" (default) | "voyage"
  model: text-embedding-3-small      # optional; uses provider default if omitted
```

New environment variables:

| Variable | Required when | Description |
|----------|--------------|-------------|
| `OPENAI_API_KEY` | `provider: openai` (default) | OpenAI API key |
| `VOYAGE_API_KEY` | `provider: voyage` | Voyage AI API key |

If no `embedding` section is present in `sidecar.yaml`, embedding is disabled — retrieval and reflect are skipped silently. Memory accumulation is opt-in.

---

## 6. Store Methods (`internal/store/memory.go`)

New types in `internal/store/memory.go`:

```go
// MemorySearchResult is a single entry returned by SearchMemory.
type MemorySearchResult struct {
    Category   string
    Content    string
    Similarity float64 // cosine similarity (0-1); 1 - (embedding <=> query)
}

// TaskEvent is a single event row from task_events.
type TaskEvent struct {
    ID        uuid.UUID
    TaskID    uuid.UUID
    Type      string
    Payload   map[string]any
    CreatedAt time.Time
}
```

New methods on `*DB`:

```go
// StoreMemory writes a memory entry with its embedding.
func (db *DB) StoreMemory(ctx context.Context, workspaceID uuid.UUID, category, content string, embedding []float32) error

// SearchMemory returns the top-k most similar memory entries for the given query embedding.
// categories filters to specified categories; nil searches all non-policy categories.
func (db *DB) SearchMemory(ctx context.Context, workspaceID uuid.UUID, categories []string, queryEmbedding []float32, limit int) ([]*MemorySearchResult, error)

// GetPolicies returns all policy rules for a workspace (no embedding needed).
func (db *DB) GetPolicies(ctx context.Context, workspaceID uuid.UUID) ([]string, error)

// StorePolicy writes a policy rule (source: "yaml" | "learned").
func (db *DB) StorePolicy(ctx context.Context, workspaceID uuid.UUID, rule, source string) error

// GetTaskEvents returns all events for a task, ordered by created_at.
func (db *DB) GetTaskEvents(ctx context.Context, taskID uuid.UUID) ([]*TaskEvent, error)
```

`SearchMemory` uses pgvector cosine distance operator `<=>`:

```sql
SELECT category, content, 1 - (embedding <=> $1) AS score
FROM memory_entries
WHERE workspace_id = $2
  AND (category = ANY($3) OR $3 IS NULL)
ORDER BY embedding <=> $1
LIMIT $4
```

---

## 7. Reflect Step (`internal/memory/reflect.go`)

Runs **asynchronously** after the output step (does not block the signal handler). A goroutine is launched with a 2-minute timeout.

```go
// Reflect extracts insights from a completed task and writes them to memory.
// Called asynchronously from Loop.Run after the output step.
func Reflect(
    ctx context.Context,
    provider EmbeddingProvider,
    llmProvider llm.LLMProvider,
    triageModel string,
    db *store.DB,
    workspace *store.Workspace,
    task *store.Task,
    events []*store.TaskEvent,  // from GetTaskEvents
) error
```

### Haiku prompt

The reflect agent receives a summary of the completed task and responds with structured JSON:

**Input:** signal type, task summary, outcome status, triage change_type + reason, key task events (triage, pr_created, suggestion payloads).

**Expected JSON response:**
```json
{
  "episodic": "Fixed failing auth tests triggered by CI failure on commit abc123. Committed to sidecar/uuid branch.",
  "semantic": [
    "auth package uses interface-based mocking",
    "integration tests require INTEGRATION=true env var"
  ],
  "procedural": [
    "run 'make test-unit' for fast unit test feedback"
  ],
  "policies": []
}
```

**Writing to store:**
- `episodic` → one `memory_entries` row (category="episodic"), embedded and stored
- `semantic` → one row per entry (category="semantic"), embedded and stored
- `procedural` → one row per entry (category="procedural"), embedded and stored
- `policies` → one row per entry in `policies` table (source="learned"), no embedding

**Fallback:** if the Haiku call fails or JSON is malformed, only the episodic entry is written (deterministically from the task record — no LLM needed for episodic).

---

## 8. Retrieval (`internal/memory/retrieve.go`)

```go
// Retrieve fetches relevant memory entries for the current signal and formats
// them as a markdown block for injection into the system prompt.
// Returns "" if memory is disabled or no relevant entries exist.
func Retrieve(
    ctx context.Context,
    provider EmbeddingProvider,
    db *store.DB,
    workspace *store.Workspace,
    query string,
    limit int,
) (string, error)
```

**Query construction:** uses the signal summary (e.g. `"fix CI failure in CI workflow @ abc123"`) as the query text. Embeds it with `inputType="query"`.

**Categories searched:** `semantic` and `procedural` (top 5 by cosine similarity, threshold ≥ 0.7).

**Policies:** loaded separately via `GetPolicies` (full list, no search).

**Formatted output injected into system prompt:**

```
## Workspace Memory

**Architecture:**
- auth package uses interface-based mocking
- integration tests require INTEGRATION=true env var

**Workflows:**
- run 'make test-unit' for fast unit test feedback

**Policies:**
- reviewer prefers explicit error handling
```

If no entries are retrieved and no policies exist, the section is omitted entirely.

---

## 9. Loop.Run Integration

Updated `Loop.Run` flow (changes marked NEW):

```
① Create task record (status: "pending")
② Triage → TriageResult
   → If !ShouldAct: status "skipped", return
③ Retrieve memory → memoryBlock string   ← NEW
④ Status "running"
⑤ Build coding agent runtime
   → BuildSystemPrompt(sig) + memoryBlock injected   ← NEW
⑥ Run coding agent, collect events
⑦ Output routing (auto-commit / pull-request / suggest-only)
⑧ Launch reflect goroutine (2-min timeout)   ← NEW
   → Reflect(ctx, embeddingProvider, ...)
```

The `Loop` struct gains two new fields:

```go
type Loop struct {
    // ... existing fields ...
    embedding memory.EmbeddingProvider // nil if embedding not configured
}
```

When `embedding` is nil, steps ③ and ⑧ are skipped silently — memory is optional.

---

## 10. Environment Variables (Phase 3 additions)

| Variable | Required when | Description |
|----------|--------------|-------------|
| `OPENAI_API_KEY` | `embedding.provider: openai` | OpenAI API key for embeddings |
| `VOYAGE_API_KEY` | `embedding.provider: voyage` | Voyage AI API key for embeddings |

---

## 11. Non-Goals for Phase 3

- `sidecar ask` query interface (Phase 4)
- Memory decay or expiry (entries accumulate indefinitely)
- Cross-workspace memory sharing
- Memory seeding from existing git history or documentation
- Policy enforcement via the memory system (policies are advisory context only)
