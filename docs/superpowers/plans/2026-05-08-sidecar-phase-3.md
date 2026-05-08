# Sidecar Phase 3 — Persistent Memory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a persistent workspace memory system — pgvector-indexed storage of architectural knowledge, workflow learnings, and policies — retrieved at plan time and populated by a reflect step after each task.

**Architecture:** A new `internal/memory/` package owns the `EmbeddingProvider` interface, OpenAI/Voyage implementations, retrieval, and reflect. A new `internal/store/memory.go` adds CRUD for `memory_entries` and `policies`. `Loop.Run` retrieves memory before building the coding agent and launches an async reflect goroutine after the output step. Memory is opt-in — if no `embedding` section is in `sidecar.yaml`, steps are skipped silently.

**Tech Stack:** Go 1.25+, pgvector PostgreSQL extension, OpenAI `text-embedding-3-small` (default, 1536 dims), Voyage AI `voyage-4` (opt-in), Harness runtime for reflect Haiku call, `net/http` for embedding API calls.

---

## File Map

```
internal/
  config/
    config.go           MODIFY — add EmbeddingConfig struct and Embedding field to Config
    config_test.go      MODIFY — add TestLoad_Embedding test
  store/
    schema.sql          MODIFY — add pgvector extension, memory_entries, policies tables
    memory.go           CREATE — MemorySearchResult, TaskEvent types + store CRUD methods
    store_test.go       MODIFY — add integration tests for new store methods
  memory/
    memory.go           CREATE — EmbeddingProvider interface + MemoryEntry type
    openai.go           CREATE — OpenAI text-embedding-3-small implementation
    openai_test.go      CREATE — mock HTTP server tests
    voyage.go           CREATE — Voyage AI voyage-4 implementation
    voyage_test.go      CREATE — mock HTTP server tests
    retrieve.go         CREATE — Retrieve() + FormatMemoryBlock()
    retrieve_test.go    CREATE — unit tests for FormatMemoryBlock, integration test for Retrieve
    reflect.go          CREATE — Reflect() + BuildReflectMessage() + ParseReflectResponse()
    reflect_test.go     CREATE — unit tests for pure functions
  loop/
    loop.go             MODIFY — add embedding field, update New(), wire retrieval + reflect
    loop_test.go        MODIFY — add tests for new status constants and CI failure prompt (already added in Phase 2)
  cli/
    attach.go           MODIFY — build EmbeddingProvider from config, pass to loop.New
    task.go             MODIFY — build EmbeddingProvider from config, pass to loop.New
```

## Environment Variables (Phase 3)

| Variable | Required when | Description |
|---|---|---|
| `OPENAI_API_KEY` | `embedding.provider: openai` | OpenAI API key |
| `VOYAGE_API_KEY` | `embedding.provider: voyage` | Voyage AI API key |

## Prerequisites

pgvector must be installed on the PostgreSQL server:
```bash
# macOS via Homebrew:
brew install pgvector
# Or via psql (superuser required):
psql -c "CREATE EXTENSION IF NOT EXISTS vector;"
```

---

### Task 1: Config — Add EmbeddingConfig

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoad_Embedding(t *testing.T) {
	yaml := `
embedding:
  provider: openai
  model: text-embedding-3-small
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, "openai", cfg.Embedding.Provider)
	assert.Equal(t, "text-embedding-3-small", cfg.Embedding.Model)
}

func TestLoad_Embedding_Empty(t *testing.T) {
	yaml := `workspace:\n  name: test\n`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.yaml")
	require.NoError(t, os.WriteFile(path, []byte("workspace:\n  name: test\n"), 0644))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, "", cfg.Embedding.Provider) // empty = memory disabled
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/config/... -v -run TestLoad_Embedding
```

Expected: FAIL — `cfg.Embedding` undefined.

- [ ] **Step 3: Add EmbeddingConfig to config.go**

Read `internal/config/config.go`. Add the new type and field:

```go
type EmbeddingConfig struct {
	Provider string `yaml:"provider"` // "openai" | "voyage"; empty = disabled
	Model    string `yaml:"model"`    // optional; uses provider default if empty
}
```

Add to `Config` struct (after `Scope ScopeConfig`):

```go
Embedding EmbeddingConfig `yaml:"embedding"`
```

- [ ] **Step 4: Run all config tests**

```bash
go test ./internal/config/... -v
```

Expected: all 8 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add EmbeddingConfig to sidecar.yaml config"
```

---

### Task 2: Schema — pgvector + Memory Tables

**Files:**
- Modify: `internal/store/schema.sql`
- Modify: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `internal/store/store_test.go` (inside the `//go:build integration` file):

```go
func TestMigrate_MemoryTables(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()

	// Should succeed and be idempotent
	require.NoError(t, store.Migrate(context.Background(), db))
	require.NoError(t, store.Migrate(context.Background(), db))

	// Verify memory_entries table exists by inserting and querying
	ws := &store.Workspace{Name: "schema-test", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	// Insert a dummy memory entry with a zero vector (just testing table exists)
	err = db.StoreMemory(context.Background(), ws.ID, "episodic", "test content", make([]float32, 1536))
	require.NoError(t, err)

	// Verify policies table exists
	err = db.StorePolicy(context.Background(), ws.ID, "test rule", "yaml")
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v -run TestMigrate_MemoryTables
```

Expected: FAIL — `StoreMemory` and `StorePolicy` undefined, and `memory_entries` table doesn't exist.

- [ ] **Step 3: Update schema.sql**

Read `internal/store/schema.sql`. Append to it:

```sql
-- Requires pgvector extension (install with: CREATE EXTENSION IF NOT EXISTS vector)
CREATE EXTENSION IF NOT EXISTS vector;

-- Workspace memory entries — embedded and retrieved by semantic similarity.
-- Used by Phase 3 to accumulate architectural knowledge, workflow learnings.
CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    category     TEXT NOT NULL,    -- "episodic" | "semantic" | "procedural"
    content      TEXT NOT NULL,
    embedding    vector(1536),     -- OpenAI text-embedding-3-small (1536 dims)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS memory_entries_embedding_idx
    ON memory_entries USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- Policy rules — advisory constraints loaded verbatim into triage/plan context.
CREATE TABLE IF NOT EXISTS policies (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    rule         TEXT NOT NULL,
    source       TEXT NOT NULL DEFAULT 'yaml',  -- "yaml" | "learned"
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Note: The `StoreMemory` and `StorePolicy` methods are implemented in Task 3. The test will still fail after this step until Task 3 is done — that's expected.

- [ ] **Step 4: Commit schema**

```bash
git add internal/store/schema.sql internal/store/store_test.go
git commit -m "feat: add memory_entries and policies tables with pgvector"
```

---

### Task 3: Store Memory Methods

**Files:**
- Create: `internal/store/memory.go`
- Modify: `internal/store/store_test.go` (add more tests)

- [ ] **Step 1: Write the failing integration tests**

Append to `internal/store/store_test.go`:

```go
func TestStoreMemory_AndSearch(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "mem-test", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	// Store a semantic memory entry with a known embedding
	embedding := make([]float32, 1536)
	embedding[0] = 1.0 // non-zero to make search work
	err = db.StoreMemory(context.Background(), ws.ID, "semantic", "auth uses interface mocking", embedding)
	require.NoError(t, err)

	// Search with the same embedding (should return the stored entry with similarity ~1.0)
	results, err := db.SearchMemory(context.Background(), ws.ID, []string{"semantic", "procedural"}, embedding, 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "semantic", results[0].Category)
	assert.Equal(t, "auth uses interface mocking", results[0].Content)
	assert.Greater(t, results[0].Similarity, 0.9)
}

func TestStorePolicy_AndGet(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "pol-test", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	require.NoError(t, db.StorePolicy(context.Background(), ws.ID, "prefer explicit error handling", "yaml"))
	require.NoError(t, db.StorePolicy(context.Background(), ws.ID, "never modify secrets/", "yaml"))

	policies, err := db.GetPolicies(context.Background(), ws.ID)
	require.NoError(t, err)
	assert.Len(t, policies, 2)
	assert.Contains(t, policies, "prefer explicit error handling")
}

func TestGetTaskEvents(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "evt-read-test", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	task := &store.Task{WorkspaceID: ws.ID, SignalType: "git.commit", Summary: "test"}
	require.NoError(t, db.CreateTask(context.Background(), task))
	require.NoError(t, db.AppendTaskEvent(context.Background(), task.ID, "triage",
		map[string]any{"should_act": true, "change_type": "bug_fix"}))

	events, err := db.GetTaskEvents(context.Background(), task.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "triage", events[0].Type)
	assert.Equal(t, true, events[0].Payload["should_act"])
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v -run "TestStoreMemory|TestStorePolicy|TestGetTaskEvents"
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement internal/store/memory.go**

Create `internal/store/memory.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MemorySearchResult is a single entry returned by SearchMemory.
type MemorySearchResult struct {
	Category   string
	Content    string
	Similarity float64 // cosine similarity in [0, 1]
}

// TaskEvent is a single event row from task_events.
type TaskEvent struct {
	ID        uuid.UUID
	TaskID    uuid.UUID
	Type      string
	Payload   map[string]any
	CreatedAt time.Time
}

// StoreMemory writes a memory entry with its embedding to memory_entries.
func (db *DB) StoreMemory(ctx context.Context, workspaceID uuid.UUID, category, content string, embedding []float32) error {
	vec := formatVector(embedding)
	_, err := db.pool.Exec(ctx, `
		INSERT INTO memory_entries (workspace_id, category, content, embedding)
		VALUES ($1, $2, $3, $4::vector)`,
		workspaceID, category, content, vec,
	)
	if err != nil {
		return fmt.Errorf("storing memory: %w", err)
	}
	return nil
}

// SearchMemory returns the top-k most similar memory entries for the given query embedding.
// categories filters results (e.g. []string{"semantic","procedural"}).
func (db *DB) SearchMemory(ctx context.Context, workspaceID uuid.UUID, categories []string, queryEmbedding []float32, limit int) ([]*MemorySearchResult, error) {
	vec := formatVector(queryEmbedding)
	rows, err := db.pool.Query(ctx, `
		SELECT category, content, 1 - (embedding <=> $1::vector) AS similarity
		FROM memory_entries
		WHERE workspace_id = $2
		  AND category = ANY($3)
		ORDER BY embedding <=> $1::vector
		LIMIT $4`,
		vec, workspaceID, categories, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("searching memory: %w", err)
	}
	defer rows.Close()

	var results []*MemorySearchResult
	for rows.Next() {
		r := &MemorySearchResult{}
		if err := rows.Scan(&r.Category, &r.Content, &r.Similarity); err != nil {
			return nil, fmt.Errorf("scanning memory row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetPolicies returns all policy rules for a workspace, ordered by creation time.
func (db *DB) GetPolicies(ctx context.Context, workspaceID uuid.UUID) ([]string, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT rule FROM policies WHERE workspace_id = $1 ORDER BY created_at`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading policies: %w", err)
	}
	defer rows.Close()

	var policies []string
	for rows.Next() {
		var rule string
		if err := rows.Scan(&rule); err != nil {
			return nil, fmt.Errorf("scanning policy: %w", err)
		}
		policies = append(policies, rule)
	}
	return policies, rows.Err()
}

// StorePolicy writes a policy rule. source is "yaml" or "learned".
func (db *DB) StorePolicy(ctx context.Context, workspaceID uuid.UUID, rule, source string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO policies (workspace_id, rule, source) VALUES ($1, $2, $3)`,
		workspaceID, rule, source,
	)
	if err != nil {
		return fmt.Errorf("storing policy: %w", err)
	}
	return nil
}

// GetTaskEvents returns all task_events for a task, ordered by created_at.
func (db *DB) GetTaskEvents(ctx context.Context, taskID uuid.UUID) ([]*TaskEvent, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, task_id, type, payload, created_at
		FROM task_events
		WHERE task_id = $1
		ORDER BY created_at`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading task events: %w", err)
	}
	defer rows.Close()

	var events []*TaskEvent
	for rows.Next() {
		ev := &TaskEvent{}
		var payloadJSON []byte
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.Type, &payloadJSON, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning task event: %w", err)
		}
		if err := json.Unmarshal(payloadJSON, &ev.Payload); err != nil {
			ev.Payload = map[string]any{}
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// formatVector converts []float32 to PostgreSQL vector literal "[f1,f2,...]".
func formatVector(v []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}
```

- [ ] **Step 4: Run the integration tests**

```bash
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v
```

Expected: all 9 store integration tests pass (5 existing + `TestMigrate_MemoryTables` + `TestStoreMemory_AndSearch` + `TestStorePolicy_AndGet` + `TestGetTaskEvents`).

- [ ] **Step 5: Commit**

```bash
git add internal/store/memory.go internal/store/store_test.go
git commit -m "feat: store methods for memory_entries, policies, and task event reads"
```

---

### Task 4: EmbeddingProvider Interface + Types

**Files:**
- Create: `internal/memory/memory.go`
- Create: `internal/memory/memory_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/memory/memory_test.go`:

```go
package memory_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestFormatMemoryBlock_WithResults(t *testing.T) {
	results := []*store.MemorySearchResult{
		{Category: "semantic", Content: "auth uses interface mocking", Similarity: 0.92},
		{Category: "procedural", Content: "run 'make test-unit' for fast tests", Similarity: 0.85},
	}
	policies := []string{"prefer explicit error handling"}

	block := memory.FormatMemoryBlock(results, policies)
	assert.Contains(t, block, "## Workspace Memory")
	assert.Contains(t, block, "auth uses interface mocking")
	assert.Contains(t, block, "make test-unit")
	assert.Contains(t, block, "prefer explicit error handling")
	assert.Contains(t, block, "**Architecture:**")
	assert.Contains(t, block, "**Workflows:**")
	assert.Contains(t, block, "**Policies:**")
}

func TestFormatMemoryBlock_BelowThreshold(t *testing.T) {
	results := []*store.MemorySearchResult{
		{Category: "semantic", Content: "low similarity entry", Similarity: 0.5},
	}
	block := memory.FormatMemoryBlock(results, nil)
	assert.Equal(t, "", block, "entries below 0.7 threshold should be excluded")
}

func TestFormatMemoryBlock_Empty(t *testing.T) {
	block := memory.FormatMemoryBlock(nil, nil)
	assert.Equal(t, "", block, "no entries and no policies → empty string")
}

func TestFormatMemoryBlock_PoliciesOnly(t *testing.T) {
	block := memory.FormatMemoryBlock(nil, []string{"never touch secrets/"})
	assert.Contains(t, block, "## Workspace Memory")
	assert.Contains(t, block, "never touch secrets/")
	assert.NotContains(t, block, "**Architecture:**")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/memory/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Create memory.go**

Create `internal/memory/memory.go`:

```go
package memory

import (
	"context"
	"strings"

	"github.com/sausheong/sidecar/internal/store"
)

const similarityThreshold = 0.7

// EmbeddingProvider converts text to vector embeddings.
type EmbeddingProvider interface {
	// Embed converts texts to float32 embedding vectors.
	// inputType is "document" (when storing) or "query" (when searching).
	// OpenAI ignores inputType; Voyage AI uses it for retrieval quality.
	Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error)
	// Dims returns the vector dimension this provider produces.
	Dims() int
}

// FormatMemoryBlock formats retrieved memory entries and policies as a markdown
// section for injection into the system prompt.
// Returns "" if no entries meet the similarity threshold and no policies exist.
func FormatMemoryBlock(results []*store.MemorySearchResult, policies []string) string {
	var semantic, procedural []string
	for _, r := range results {
		if r.Similarity < similarityThreshold {
			continue
		}
		switch r.Category {
		case "semantic":
			semantic = append(semantic, r.Content)
		case "procedural":
			procedural = append(procedural, r.Content)
		}
	}

	if len(semantic) == 0 && len(procedural) == 0 && len(policies) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Workspace Memory\n")

	if len(semantic) > 0 {
		sb.WriteString("\n**Architecture:**\n")
		for _, s := range semantic {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(procedural) > 0 {
		sb.WriteString("\n**Workflows:**\n")
		for _, s := range procedural {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(policies) > 0 {
		sb.WriteString("\n**Policies:**\n")
		for _, p := range policies {
			sb.WriteString("- " + p + "\n")
		}
	}
	return sb.String()
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/memory/... -v
```

Expected: all 4 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat: EmbeddingProvider interface and FormatMemoryBlock"
```

---

### Task 5: OpenAI Embedding Implementation

**Files:**
- Create: `internal/memory/openai.go`
- Create: `internal/memory/openai_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/memory/openai_test.go`:

```go
package memory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sausheong/sidecar/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIProvider_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		require.Equal(t, "Bearer testkey", r.Header.Get("Authorization"))

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "text-embedding-3-small", body["model"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": make([]float32, 1536), "index": 0},
			},
		})
	}))
	defer server.Close()

	p := memory.NewOpenAIWithBaseURL("testkey", "text-embedding-3-small", server.URL)
	result, err := p.Embed(context.Background(), []string{"hello"}, "document")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0], 1536)
}

func TestOpenAIProvider_Dims(t *testing.T) {
	p := memory.NewOpenAI("key", "")
	assert.Equal(t, 1536, p.Dims())
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/memory/... -v -run TestOpenAI
```

Expected: FAIL — `NewOpenAI`, `NewOpenAIWithBaseURL` undefined.

- [ ] **Step 3: Implement openai.go**

Create `internal/memory/openai.go`:

```go
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const openAIBase = "https://api.openai.com"
const openAIDefaultModel = "text-embedding-3-small"

// OpenAIProvider embeds text using OpenAI's embedding API.
type OpenAIProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewOpenAI creates a provider using the OpenAI API.
func NewOpenAI(apiKey, model string) *OpenAIProvider {
	return NewOpenAIWithBaseURL(apiKey, model, openAIBase)
}

// NewOpenAIWithBaseURL creates a provider with a custom base URL (used in tests).
func NewOpenAIWithBaseURL(apiKey, model, baseURL string) *OpenAIProvider {
	if model == "" {
		model = openAIDefaultModel
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Dims returns 1536 (text-embedding-3-small fixed dimension).
func (p *OpenAIProvider) Dims() int { return 1536 }

// Embed calls the OpenAI embeddings API. inputType is ignored (OpenAI has no query/document distinction).
func (p *OpenAIProvider) Embed(ctx context.Context, texts []string, _ string) ([][]float32, error) {
	payload := map[string]any{
		"input": texts,
		"model": p.model,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embed status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding openai response: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	return embeddings, nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/memory/... -v
```

Expected: all 6 tests pass (4 from Task 4 + 2 new).

- [ ] **Step 5: Commit**

```bash
git add internal/memory/openai.go internal/memory/openai_test.go
git commit -m "feat: OpenAI text-embedding-3-small provider"
```

---

### Task 6: Voyage AI Embedding Implementation

**Files:**
- Create: `internal/memory/voyage.go`
- Create: `internal/memory/voyage_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/memory/voyage_test.go`:

```go
package memory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sausheong/sidecar/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVoyageProvider_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		require.Equal(t, "Bearer voyagekey", r.Header.Get("Authorization"))

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "voyage-4", body["model"])
		assert.Equal(t, "document", body["input_type"])
		assert.Equal(t, float64(1536), body["output_dimension"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": make([]float32, 1536), "index": 0},
			},
		})
	}))
	defer server.Close()

	p := memory.NewVoyageWithBaseURL("voyagekey", "voyage-4", server.URL)
	result, err := p.Embed(context.Background(), []string{"test text"}, "document")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0], 1536)
}

func TestVoyageProvider_Dims(t *testing.T) {
	p := memory.NewVoyage("key", "")
	assert.Equal(t, 1536, p.Dims())
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/memory/... -v -run TestVoyage
```

Expected: FAIL — `NewVoyage`, `NewVoyageWithBaseURL` undefined.

- [ ] **Step 3: Implement voyage.go**

Create `internal/memory/voyage.go`:

```go
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const voyageBase = "https://api.voyageai.com"
const voyageDefaultModel = "voyage-4"

// VoyageProvider embeds text using Voyage AI's embedding API.
// Output is fixed at 1536 dimensions to match the schema.
type VoyageProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewVoyage creates a provider using the Voyage AI API.
func NewVoyage(apiKey, model string) *VoyageProvider {
	return NewVoyageWithBaseURL(apiKey, model, voyageBase)
}

// NewVoyageWithBaseURL creates a provider with a custom base URL (used in tests).
func NewVoyageWithBaseURL(apiKey, model, baseURL string) *VoyageProvider {
	if model == "" {
		model = voyageDefaultModel
	}
	return &VoyageProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Dims returns 1536 (output_dimension configured to match the schema).
func (p *VoyageProvider) Dims() int { return 1536 }

// Embed calls the Voyage AI embeddings API.
// inputType is "document" (when storing) or "query" (when searching).
func (p *VoyageProvider) Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	payload := map[string]any{
		"input":            texts,
		"model":            p.model,
		"input_type":       inputType,
		"output_dimension": 1536, // must match memory_entries.embedding dimension
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling voyage request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("voyage embed status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding voyage response: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	return embeddings, nil
}
```

- [ ] **Step 4: Run all memory tests**

```bash
go test ./internal/memory/... -v
```

Expected: all 8 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/memory/voyage.go internal/memory/voyage_test.go
git commit -m "feat: Voyage AI voyage-4 embedding provider"
```

---

### Task 7: Retrieval

**Files:**
- Create: `internal/memory/retrieve.go`
- Modify: `internal/memory/memory_test.go`

- [ ] **Step 1: Write the failing test**

The `FormatMemoryBlock` tests already cover the output formatting. Add one test for `Retrieve` using a mock provider:

Append to `internal/memory/memory_test.go`:

```go
// mockProvider is a test double for EmbeddingProvider.
type mockProvider struct{}

func (m *mockProvider) Dims() int { return 1536 }
func (m *mockProvider) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, 1536)
		v[0] = 1.0 // non-zero so it works with pgvector
		result[i] = v
	}
	return result, nil
}

func TestBuildQueryFromSummary(t *testing.T) {
	// Retrieve uses the signal summary as the query — just verify FormatMemoryBlock integration
	results := []*store.MemorySearchResult{
		{Category: "semantic", Content: "auth is fragile", Similarity: 0.95},
	}
	block := memory.FormatMemoryBlock(results, nil)
	assert.Contains(t, block, "auth is fragile")
}
```

- [ ] **Step 2: Run to confirm it passes already (FormatMemoryBlock is tested)**

```bash
go test ./internal/memory/... -v -run TestBuildQueryFromSummary
```

Expected: PASS (FormatMemoryBlock is already implemented).

- [ ] **Step 3: Implement retrieve.go**

Create `internal/memory/retrieve.go`:

```go
package memory

import (
	"context"
	"fmt"

	"github.com/sausheong/sidecar/internal/store"
)

const defaultRetrieveLimit = 5

// Retrieve fetches relevant memory entries for the current signal query and formats
// them as a markdown block for injection into the system prompt.
// Returns "" if the provider is nil, memory is empty, or no entries are relevant.
func Retrieve(ctx context.Context, provider EmbeddingProvider, db *store.DB, workspace *store.Workspace, query string, limit int) (string, error) {
	if provider == nil {
		return "", nil
	}
	if limit <= 0 {
		limit = defaultRetrieveLimit
	}

	// Embed the query using "query" input type for retrieval-optimized vectors.
	embeddings, err := provider.Embed(ctx, []string{query}, "query")
	if err != nil {
		return "", fmt.Errorf("embedding retrieval query: %w", err)
	}
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return "", nil
	}

	results, err := db.SearchMemory(ctx, workspace.ID, []string{"semantic", "procedural"}, embeddings[0], limit)
	if err != nil {
		return "", fmt.Errorf("searching memory: %w", err)
	}

	policies, err := db.GetPolicies(ctx, workspace.ID)
	if err != nil {
		return "", fmt.Errorf("loading policies: %w", err)
	}

	return FormatMemoryBlock(results, policies), nil
}
```

- [ ] **Step 4: Run all memory tests**

```bash
go test ./internal/memory/... -v
```

Expected: all 9 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/memory/retrieve.go internal/memory/memory_test.go
git commit -m "feat: memory retrieval with pgvector cosine similarity"
```

---

### Task 8: Reflect Step

**Files:**
- Create: `internal/memory/reflect.go`
- Create: `internal/memory/reflect_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/memory/reflect_test.go`:

```go
package memory_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReflectResponse_Valid(t *testing.T) {
	raw := `{"episodic":"Fixed failing auth tests.","semantic":["auth uses interface mocking"],"procedural":["run make test-unit"],"policies":[]}`
	resp, err := memory.ParseReflectResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "Fixed failing auth tests.", resp.Episodic)
	assert.Equal(t, []string{"auth uses interface mocking"}, resp.Semantic)
	assert.Equal(t, []string{"run make test-unit"}, resp.Procedural)
	assert.Empty(t, resp.Policies)
}

func TestParseReflectResponse_Invalid(t *testing.T) {
	_, err := memory.ParseReflectResponse("not json")
	assert.Error(t, err)
}

func TestBuildReflectMessage(t *testing.T) {
	task := &store.Task{
		SignalType: "ci.failure",
		Summary:   "fix CI failure in CI @ abc123",
		Status:    "completed",
	}
	events := []*store.TaskEvent{
		{
			ID:      uuid.New(),
			TaskID:  task.ID,
			Type:    "triage",
			Payload: map[string]any{"should_act": true, "change_type": "test_fix"},
		},
	}
	msg := memory.BuildReflectMessage(task, events)
	assert.Contains(t, msg, "ci.failure")
	assert.Contains(t, msg, "fix CI failure")
	assert.Contains(t, msg, "triage")
	assert.Contains(t, msg, "test_fix")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/memory/... -v -run "TestParseReflect|TestBuildReflect"
```

Expected: FAIL — `ParseReflectResponse`, `BuildReflectMessage` undefined.

- [ ] **Step 3: Implement reflect.go**

Create `internal/memory/reflect.go`:

```go
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/sidecar/internal/store"
)

const reflectSystemPrompt = `You are a memory extraction agent for an autonomous software engineering system.
Given a summary of a completed engineering task, extract useful knowledge.

Respond with ONLY valid JSON in this exact format:
{
  "episodic": "one sentence: what was done and the outcome",
  "semantic": ["architectural insight 1"],
  "procedural": ["specific command or workflow that worked"],
  "policies": []
}

Guidelines:
- episodic: always provide a factual one-sentence summary (never omit)
- semantic: code patterns, conventions, fragile areas discovered (may be empty array)
- procedural: specific commands or steps confirmed to work, e.g. "run 'make test' not 'go test ./...'" (may be empty array)
- policies: constraints learned from PR reviewer feedback (usually empty)
- Keep each entry to one concise sentence
- Respond with JSON only — no prose`

// ReflectResponse is the structured output of the reflect agent.
type ReflectResponse struct {
	Episodic   string   `json:"episodic"`
	Semantic   []string `json:"semantic"`
	Procedural []string `json:"procedural"`
	Policies   []string `json:"policies"`
}

// ParseReflectResponse parses the reflect agent's JSON response.
// Exported for testing.
func ParseReflectResponse(raw string) (ReflectResponse, error) {
	raw = strings.TrimSpace(raw)
	var resp ReflectResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return ReflectResponse{}, fmt.Errorf("parsing reflect JSON: %w", err)
	}
	return resp, nil
}

// BuildReflectMessage constructs the user message for the reflect agent.
// Exported for testing.
func BuildReflectMessage(task *store.Task, events []*store.TaskEvent) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Signal: %s\nSummary: %s\nStatus: %s\n",
		task.SignalType, task.Summary, task.Status))
	for _, ev := range events {
		if ev.Type == "triage" || ev.Type == "pr_created" || ev.Type == "suggestion" {
			data, _ := json.Marshal(ev.Payload)
			sb.WriteString(fmt.Sprintf("Event [%s]: %s\n", ev.Type, string(data)))
		}
	}
	sb.WriteString("\nExtract memory from this completed task.")
	return sb.String()
}

// Reflect extracts insights from a completed task and writes them to memory.
// Runs asynchronously — caller should launch in a goroutine with a timeout context.
func Reflect(
	ctx context.Context,
	provider EmbeddingProvider,
	llmProvider llm.LLMProvider,
	triageModel string,
	db *store.DB,
	workspace *store.Workspace,
	task *store.Task,
	events []*store.TaskEvent,
) error {
	msg := BuildReflectMessage(task, events)

	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: llmProvider,
			Tools:    tool.NewRegistry(),
			Session:  session.NewSession("reflect-"+uuid.New().String(), "main"),
		},
		runtime.AgentSpec{
			ID:           "reflect",
			Name:         "Reflect",
			Model:        triageModel,
			SystemPrompt: reflectSystemPrompt,
			MaxTurns:     1,
		},
	)
	if err != nil {
		slog.Warn("reflect: runtime build failed, storing episodic only", "err", err)
		return storeEpisodicFallback(ctx, provider, db, workspace, task)
	}
	defer rt.Close()

	evts, err := rt.Run(ctx, msg, nil)
	if err != nil {
		slog.Warn("reflect: run failed, storing episodic only", "err", err)
		return storeEpisodicFallback(ctx, provider, db, workspace, task)
	}

	var sb strings.Builder
	for ev := range evts {
		if ev.Type == runtime.EventTextDelta {
			sb.WriteString(ev.Text)
		}
	}

	resp, err := ParseReflectResponse(sb.String())
	if err != nil {
		slog.Warn("reflect: parse failed, storing episodic only", "err", err, "raw", sb.String())
		return storeEpisodicFallback(ctx, provider, db, workspace, task)
	}

	return storeReflectResponse(ctx, provider, db, workspace, resp)
}

func storeReflectResponse(ctx context.Context, provider EmbeddingProvider, db *store.DB, workspace *store.Workspace, resp ReflectResponse) error {
	// Batch all text entries that need embedding.
	var texts, categories []string
	if resp.Episodic != "" {
		texts = append(texts, resp.Episodic)
		categories = append(categories, "episodic")
	}
	for _, s := range resp.Semantic {
		texts = append(texts, s)
		categories = append(categories, "semantic")
	}
	for _, p := range resp.Procedural {
		texts = append(texts, p)
		categories = append(categories, "procedural")
	}

	if len(texts) > 0 {
		embeddings, err := provider.Embed(ctx, texts, "document")
		if err != nil {
			return fmt.Errorf("embedding reflect entries: %w", err)
		}
		for i, content := range texts {
			if i < len(embeddings) {
				if err := db.StoreMemory(ctx, workspace.ID, categories[i], content, embeddings[i]); err != nil {
					slog.Warn("reflect: failed to store memory entry", "category", categories[i], "err", err)
				}
			}
		}
	}

	for _, p := range resp.Policies {
		if err := db.StorePolicy(ctx, workspace.ID, p, "learned"); err != nil {
			slog.Warn("reflect: failed to store policy", "err", err)
		}
	}
	return nil
}

// storeEpisodicFallback writes a deterministic episodic entry when the LLM call fails.
func storeEpisodicFallback(ctx context.Context, provider EmbeddingProvider, db *store.DB, workspace *store.Workspace, task *store.Task) error {
	content := fmt.Sprintf("%s: %s → %s", task.SignalType, task.Summary, task.Status)
	embeddings, err := provider.Embed(ctx, []string{content}, "document")
	if err != nil {
		return fmt.Errorf("embedding episodic fallback: %w", err)
	}
	if len(embeddings) == 0 {
		return nil
	}
	return db.StoreMemory(ctx, workspace.ID, "episodic", content, embeddings[0])
}
```

- [ ] **Step 4: Run all memory tests**

```bash
go test ./internal/memory/... -v
```

Expected: all 12 tests pass (9 existing + 3 new).

- [ ] **Step 5: Run full build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/memory/reflect.go internal/memory/reflect_test.go
git commit -m "feat: reflect step — Haiku extracts memory insights after each task"
```

---

### Task 9: Loop Integration

**Files:**
- Modify: `internal/loop/loop.go`
- Modify: `internal/loop/loop_test.go`

- [ ] **Step 1: Write the failing test**

Read `internal/loop/loop_test.go`. Add this test:

```go
func TestLoop_MemoryNilSafe(t *testing.T) {
	// When embedding is nil, loop.New should not panic
	// and ResolveModels should still work
	cfg := &config.Config{}
	models := loop.ResolveModels(cfg)
	assert.NotEmpty(t, models.Coding)
	// The nil embedding path is exercised implicitly in other tests
	// since all existing tests pass nil for the embedding provider
}
```

- [ ] **Step 2: Run to confirm it passes already**

```bash
go test ./internal/loop/... -v -run TestLoop_MemoryNilSafe
```

Expected: FAIL (because `loop.New` currently takes 4 args and we're not changing the signature yet). After the next step it should pass.

- [ ] **Step 3: Update loop.go**

Read `internal/loop/loop.go`. Apply these changes:

**3a. Add imports:**
```go
"time"

"github.com/sausheong/sidecar/internal/memory"
```

**3b. Update `Loop` struct** — add `embedding` field:
```go
type Loop struct {
	db        *store.DB
	workspace *store.Workspace
	cfg       *config.Config
	repoPath  string
	provider  llm.LLMProvider
	embedding memory.EmbeddingProvider // nil when memory is not configured
}
```

**3c. Update `New` function** — add `embedding` parameter:
```go
// New constructs a Loop. Pass nil for embedding to disable memory retrieval and reflect.
func New(db *store.DB, workspace *store.Workspace, cfg *config.Config, repoPath string, embedding memory.EmbeddingProvider) *Loop {
	return &Loop{
		db:        db,
		workspace: workspace,
		cfg:       cfg,
		repoPath:  repoPath,
		provider:  anthropic.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), ""),
		embedding: embedding,
	}
}
```

**3d. In `Run`, add memory retrieval after triage (after the `!tr.ShouldAct` check, before `UpdateTaskStatus(Running)`):**

```go
// ── Memory retrieval ─────────────────────────────────────────────────────────
var memoryBlock string
if l.embedding != nil {
    block, mErr := memory.Retrieve(ctx, l.embedding, l.db, l.workspace, task.Summary, 5)
    if mErr != nil {
        slog.Warn("memory retrieval failed", "err", mErr, "task", task.ID)
    } else {
        memoryBlock = block
    }
}
```

**3e. Augment the system prompt with the memory block.** Find the line `SystemPrompt: BuildSystemPrompt(sig),` and replace it:

```go
systemPrompt := BuildSystemPrompt(sig)
if memoryBlock != "" {
    systemPrompt = memoryBlock + "\n\n" + systemPrompt
}
```

Then in the `AgentSpec`:
```go
SystemPrompt: systemPrompt,
```

**3f. After the output routing switch (at the very end of `Run`, before the final `return`), add the async reflect goroutine:**

```go
// ── Reflect (async) ──────────────────────────────────────────────────────────
if l.embedding != nil {
    go func() {
        reflectCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
        defer cancel()
        events, err := l.db.GetTaskEvents(reflectCtx, task.ID)
        if err != nil {
            slog.Warn("reflect: failed to load task events", "err", err, "task", task.ID)
            return
        }
        models := ResolveModels(l.cfg)
        if err := memory.Reflect(reflectCtx, l.embedding, l.provider, models.Triage, l.db, l.workspace, task, events); err != nil {
            slog.Warn("reflect failed", "err", err, "task", task.ID)
        }
    }()
}
```

Note: the reflect goroutine runs after each output path. Move it to be the very last statement in `Run` before the final `return` from each output branch, OR refactor so it runs once after the switch. The cleanest approach: return from each switch case via a helper that also launches reflect. Simpler: use a `defer`-like pattern — set a `finalStatus` variable, break out of the switch, launch reflect, then return. Here is the cleanest approach: add the reflect goroutine immediately before each `return l.db.UpdateTaskStatus(...)` call in the switch, or refactor `Run` to use a single exit point:

```go
// Replace the entire switch with this pattern:
finalStatus, finalErr := l.runOutput(ctx, tr, task, sig, textBuf.String())
// Launch reflect regardless of output result
if l.embedding != nil {
    go func() { /* ... reflect ... */ }()
}
return l.db.UpdateTaskStatus(ctx, task.ID, finalStatus)
```

This is cleaner but requires extracting the switch into `runOutput`. For Phase 3 simplicity, add the reflect goroutine once, just before the final status update in each branch. The switch already has three paths — add the goroutine launch at the start of each branch before returning, or use the single-exit-point pattern above. Choose whichever is cleaner given the current code shape.

- [ ] **Step 4: Run all loop tests**

```bash
go test ./internal/loop/... -v
```

Expected: all loop tests pass.

- [ ] **Step 5: Run full build**

```bash
go build ./...
```

Expected: FAIL on `internal/cli/attach.go` and `internal/cli/task.go` — `loop.New` now requires 5 arguments. This is expected; Task 10 fixes it.

- [ ] **Step 6: Commit (even with build errors — Task 10 fixes them)**

```bash
git add internal/loop/loop.go internal/loop/loop_test.go
git commit -m "feat: loop retrieval + async reflect goroutine"
```

---

### Task 10: Attach + Task CLI Wiring

**Files:**
- Modify: `internal/cli/attach.go`
- Modify: `internal/cli/task.go`
- Modify: `internal/cli/attach_test.go`

- [ ] **Step 1: Write the failing test**

Read `internal/cli/attach_test.go`. Add:

```go
func TestAttachCmd_MemoryDisabledWithoutEmbeddingConfig(t *testing.T) {
	// sidecar.yaml with no embedding section → attach still works, memory just disabled
	dir := t.TempDir()
	yaml := `
workspace:
  name: test
signals:
  - adapter: git
    watch: [push]
autonomy:
  test_fixes: auto-commit
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sidecar.yaml"), []byte(yaml), 0644))

	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"attach", dir})
	err := root.Execute()
	// Should fail on SIDECAR_DB_URL, not on embedding config
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/cli/... -v -run TestAttachCmd_MemoryDisabled
```

Expected: FAIL — compile error because `loop.New` signature changed.

- [ ] **Step 3: Update attach.go**

Read `internal/cli/attach.go`. Add imports:

```go
"github.com/sausheong/sidecar/internal/memory"
```

Add a helper function `buildEmbeddingProvider` before the end of the file:

```go
// buildEmbeddingProvider constructs an EmbeddingProvider from config.
// Returns nil if embedding is not configured, which disables memory.
func buildEmbeddingProvider(cfg *config.Config) memory.EmbeddingProvider {
	switch cfg.Embedding.Provider {
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			log.Printf("warning: embedding.provider=openai but OPENAI_API_KEY not set; memory disabled")
			return nil
		}
		return memory.NewOpenAI(apiKey, cfg.Embedding.Model)
	case "voyage":
		apiKey := os.Getenv("VOYAGE_API_KEY")
		if apiKey == "" {
			log.Printf("warning: embedding.provider=voyage but VOYAGE_API_KEY not set; memory disabled")
			return nil
		}
		return memory.NewVoyage(apiKey, cfg.Embedding.Model)
	default:
		return nil // memory disabled
	}
}
```

Find the line `l := loop.New(db, ws, cfg, abs)` in `attachCmd` and replace it:

```go
embeddingProvider := buildEmbeddingProvider(cfg)
l := loop.New(db, ws, cfg, abs, embeddingProvider)
```

- [ ] **Step 4: Update task.go**

Read `internal/cli/task.go`. Find the line `l := loop.New(db, ws, cfg, abs)` and replace it:

```go
embeddingProvider := buildEmbeddingProvider(cfg)
l := loop.New(db, ws, cfg, abs, embeddingProvider)
```

Also add the `buildEmbeddingProvider` function to `task.go` — OR move it to a shared helper file `internal/cli/embedding.go`. The cleanest approach: create `internal/cli/embedding.go` with `buildEmbeddingProvider` and import it from both commands.

Create `internal/cli/embedding.go`:

```go
package cli

import (
	"log"
	"os"

	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/memory"
)

// buildEmbeddingProvider constructs an EmbeddingProvider from the embedding config.
// Returns nil if embedding is not configured — memory is then disabled silently.
func buildEmbeddingProvider(cfg *config.Config) memory.EmbeddingProvider {
	switch cfg.Embedding.Provider {
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			log.Printf("warning: embedding.provider=openai but OPENAI_API_KEY not set; memory disabled")
			return nil
		}
		return memory.NewOpenAI(apiKey, cfg.Embedding.Model)
	case "voyage":
		apiKey := os.Getenv("VOYAGE_API_KEY")
		if apiKey == "" {
			log.Printf("warning: embedding.provider=voyage but VOYAGE_API_KEY not set; memory disabled")
			return nil
		}
		return memory.NewVoyage(apiKey, cfg.Embedding.Model)
	default:
		return nil
	}
}
```

Then in both `attach.go` and `task.go`, just call:
```go
l := loop.New(db, ws, cfg, abs, buildEmbeddingProvider(cfg))
```

Remove the `"github.com/sausheong/sidecar/internal/memory"` import from `attach.go` (it's now in `embedding.go`) if the compiler complains about duplicate symbols.

- [ ] **Step 5: Run all CLI tests**

```bash
go test ./internal/cli/... -v
```

Expected: all tests pass.

- [ ] **Step 6: Run full test suite and build**

```bash
go test ./...
go build -o /tmp/sidecar ./cmd/sidecar
```

Expected: all packages pass, binary builds.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/attach.go internal/cli/task.go internal/cli/embedding.go internal/cli/attach_test.go
git commit -m "feat: wire embedding provider into loop from sidecar.yaml config"
```

---

## Verification

After all tasks complete, verify end-to-end with embedding enabled:

```bash
export SIDECAR_DB_URL="postgres://localhost/sidecar"
export ANTHROPIC_API_KEY="..."
export OPENAI_API_KEY="..."

# Add embedding to sidecar.yaml
cat >> /path/to/repo/sidecar.yaml << 'EOF'
embedding:
  provider: openai
  model: text-embedding-3-small
EOF

go build -o /tmp/sidecar ./cmd/sidecar

# Attach and run a task (reflect will store episodic memory)
cd /path/to/repo
/tmp/sidecar task "review recent changes and check test coverage"

# Run a second task — retrieval should surface the first task's memory
/tmp/sidecar task "fix any failing tests"
/tmp/sidecar status
```

Integration tests:
```bash
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v
```
