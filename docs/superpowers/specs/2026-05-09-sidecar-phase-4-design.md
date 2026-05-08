# Sidecar Phase 4 — Proactive Runtime Design Spec

**Date:** 2026-05-09
**Status:** Approved
**Builds on:** Phase 1–3 (core runtime, reactive CI, persistent memory — complete)

---

## 1. Overview

Phase 4 makes Sidecar proactive in two ways:

1. **Memory-guided idle sweep** — the existing scheduled sweep (via `SignalScheduleTick`) is made smarter by rewriting its system prompt to explicitly use the `## Workspace Memory` block already injected by Phase 3. The agent prioritises fragile areas and undertested paths from memory before falling back to generic checks.

2. **`sidecar ask`** — a new CLI command that answers natural-language questions about the codebase by retrieving relevant memory entries and synthesising a coherent answer via a Haiku agent.

**Key principle:** Both features are lightweight extensions of Phase 3 infrastructure. No new packages are required for the sweep improvement (one prompt change). `sidecar ask` adds two files: `internal/memory/ask.go` and `internal/cli/ask.go`.

---

## 2. New Components

```
internal/
  memory/
    ask.go          Ask() — retrieves memory + synthesises answer with Haiku
    ask_test.go
  cli/
    ask.go          sidecar ask command
    ask_test.go
```

**Modified files:**
- `internal/loop/loop.go` — update `BuildSystemPrompt` for `SignalScheduleTick`
- `internal/cli/root.go` — add `askCmd()` to root command

---

## 3. Memory-Guided Sweep Prompt

Replace the `SignalScheduleTick` case in `BuildSystemPrompt` (currently in `internal/loop/loop.go`).

**Before (Phase 3):**
```
This is a proactive maintenance sweep. Look for improvement opportunities:
- Stale or vulnerable dependencies
- Missing test coverage for existing code paths
- Outdated documentation
- Dead code or unused imports
Pick one meaningful improvement and apply it.
```

**After (Phase 4):**
```
This is a proactive maintenance sweep.

The ## Workspace Memory section above contains what is known about this codebase —
fragile areas, past failures, and areas noted as undertested.

Use that knowledge to guide your choice. In priority order:
1. Fix or improve any area flagged as fragile or prone to regression
2. Add tests to code paths noted as missing coverage
3. If no specific area is flagged, check for: stale dependencies, dead code, or outdated docs

Pick ONE meaningful improvement and apply it. Run tests to verify your change.
```

**Behaviour when memory is empty (new workspace):** Point 3 of the priority list ensures the agent still does useful work — the generic sweep items act as a natural fallback.

This change is a pure string update to `BuildSystemPrompt`. No structural changes to the loop, triage, or scheduling logic.

---

## 4. `sidecar ask` Command

### CLI Interface

```bash
sidecar ask "how does auth work"
sidecar ask "which areas are fragile" --repo /path/to/repo
```

**Flags:**
- `--repo string` — path to the target repository (default: current working directory)

**Requirements:**
- `SIDECAR_DB_URL` — required (workspace lookup)
- `OPENAI_API_KEY` or `VOYAGE_API_KEY` — required (embedding the query)
- `ANTHROPIC_API_KEY` — required (Haiku synthesis call)

**Error cases:**
- Workspace not found for repo path → exit with "run 'sidecar attach' first"
- No memory entries in workspace → exit with "no memory found for this workspace yet; run some tasks first"
- Missing env vars → clear error message per missing var

### Ask Flow

```
① Embed the query (inputType="query", using configured embedding provider)
② Retrieve top 8 memory entries across all categories (semantic, procedural, episodic)
   via SearchMemory — same pgvector cosine similarity as Phase 3 retrieval
③ Load all policy rules via GetPolicies
④ If no entries and no policies → return "no memory found" error
⑤ Call Haiku synthesis agent (MaxTurns=1, no tools) with memory context + question
⑥ Print synthesised answer to stdout
```

### `internal/memory/ask.go`

```go
// Ask retrieves workspace memory relevant to the query and synthesises a
// natural-language answer using a Haiku agent.
// Returns an error if no memory exists for the workspace.
func Ask(
    ctx context.Context,
    provider EmbeddingProvider,
    llmProvider llm.LLMProvider,
    model string,
    db *store.DB,
    workspace *store.Workspace,
    query string,
) (string, error)
```

**Synthesis system prompt:**
```
You are a knowledgeable assistant for a software project. You have access to the project's
accumulated memory — facts, patterns, and history extracted from real engineering work.

Answer the developer's question using ONLY the information provided in the memory below.
Be concise, direct, and accurate. If the memory doesn't contain enough information to
answer confidently, say so explicitly rather than guessing.
```

**User message format:**
```
Memory:
{formatted memory block — same FormatMemoryBlock output as Phase 3, but with all categories including episodic}

Question: {query}
```

**Retrieval difference from Phase 3:** `Ask` searches all three categories (`semantic`, `procedural`, `episodic`) with a higher limit (8 entries) rather than just `semantic` and `procedural` at 5. Episodic entries help answer historical questions like "when did we fix the auth bug?".

### `internal/cli/ask.go`

```go
func askCmd() *cobra.Command
```

- `--repo` flag (default: CWD)
- Validates `SIDECAR_DB_URL`, `ANTHROPIC_API_KEY`, and at least one embedding key
- Loads `sidecar.yaml` from the repo path (fallback to `&config.Config{}` if absent, same as `task.go`)
- Calls `buildEmbeddingProvider(cfg)` (existing helper from `embedding.go`); exits with error if provider is nil (no embedding configured)
- Calls `memory.Ask(...)` and prints the result to stdout
- On error: prints a user-friendly message and exits non-zero

---

## 5. Updated CLI

Add `askCmd()` to `root.go`:

```go
root.AddCommand(attachCmd(), taskCmd(), statusCmd(), askCmd())
```

Usage after Phase 4:
```
sidecar attach .
sidecar task "fix the flaky test"
sidecar status
sidecar ask "how does authentication work?"
```

---

## 6. Non-Goals for Phase 4

- Interactive conversational mode (`sidecar ask` is single-shot, not multi-turn)
- Storing ask queries as memory entries (queries are read-only)
- Sweep scheduling configuration (interval still set via `sidecar.yaml` cron)
- Sweep history or deduplication (same improvement may be suggested repeatedly)
- `sidecar stop` command (daemon is stopped via SIGTERM/SIGINT to the process)
