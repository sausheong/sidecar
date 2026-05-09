# Sidecar Phase 4 — Proactive Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the idle sweep memory-guided and add `sidecar ask` for querying workspace knowledge.

**Architecture:** Two changes to the existing system: (1) rewrite the `SignalScheduleTick` case in `BuildSystemPrompt` to explicitly direct the agent to use the `## Workspace Memory` block already injected by Phase 3; (2) add `internal/memory/ask.go` with a `Ask()` function (retrieval + Haiku synthesis) and `internal/cli/ask.go` as the CLI command that calls it.

**Tech Stack:** Go 1.25+, existing Harness runtime (MaxTurns=1 Haiku call), existing pgvector retrieval from Phase 3, existing cobra CLI pattern.

---

## File Map

```
internal/
  loop/
    loop.go           MODIFY — update BuildSystemPrompt SignalScheduleTick case
    loop_test.go      MODIFY — add TestBuildSystemPrompt_ScheduleTick_MemoryGuided
  memory/
    ask.go            CREATE — Ask(), BuildAskMessage() (exported for testing)
    ask_test.go       CREATE — unit tests for BuildAskMessage
  cli/
    ask.go            CREATE — askCmd() cobra command
    ask_test.go       CREATE — TestAskCmd_RequiresDBURL
    root.go           MODIFY — add askCmd() to root
```

---

### Task 1: Update Memory-Guided Sweep Prompt

**Files:**
- Modify: `internal/loop/loop.go`
- Modify: `internal/loop/loop_test.go`

- [ ] **Step 1: Write the failing test**

Read `internal/loop/loop_test.go`. Add this test:

```go
func TestBuildSystemPrompt_ScheduleTick_MemoryGuided(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalScheduleTick,
		Source:  "schedule",
		Payload: map[string]any{},
	}
	prompt := loop.BuildSystemPrompt(sig)
	// Phase 4: prompt must explicitly reference workspace memory
	assert.Contains(t, prompt, "Workspace Memory")
	assert.Contains(t, prompt, "fragile")
	assert.Contains(t, prompt, "engineering agent")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/loop/... -v -run TestBuildSystemPrompt_ScheduleTick_MemoryGuided
```

Expected: FAIL — the current sweep prompt does not contain "Workspace Memory" or "fragile".

- [ ] **Step 3: Update the sweep prompt in loop.go**

Read `internal/loop/loop.go`. Find the `SignalScheduleTick` case inside `BuildSystemPrompt` and replace it:

```go
case adapter.SignalScheduleTick:
    return fmt.Sprintf(`%s

This is a proactive maintenance sweep.

The ## Workspace Memory section above contains what is known about this codebase —
fragile areas, past failures, and areas noted as undertested.

Use that knowledge to guide your choice. In priority order:
1. Fix or improve any area flagged as fragile or prone to regression
2. Add tests to code paths noted as missing coverage
3. If no specific area is flagged, check for: stale dependencies, dead code, or outdated docs

Pick ONE meaningful improvement and apply it. Run tests to verify your change.`, base)
```

- [ ] **Step 4: Run all loop tests**

```bash
go test ./internal/loop/... -v
```

Expected: all 8 tests pass (7 existing + 1 new).

- [ ] **Step 5: Commit**

```bash
git add internal/loop/loop.go internal/loop/loop_test.go
git commit -m "feat: memory-guided sweep prompt uses workspace knowledge"
```

---

### Task 2: `Ask()` Function

**Files:**
- Create: `internal/memory/ask.go`
- Create: `internal/memory/ask_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/memory/ask_test.go`:

```go
package memory_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestBuildAskMessage_WithMemory(t *testing.T) {
	entries := []*store.MemorySearchResult{
		{Category: "semantic", Content: "auth uses interface mocking", Similarity: 0.92},
		{Category: "procedural", Content: "run make test-unit for fast tests", Similarity: 0.85},
		{Category: "episodic", Content: "Fixed auth bug on 2026-05-01. Root cause: stale mock.", Similarity: 0.80},
	}
	policies := []string{"prefer explicit error handling"}
	msg := memory.BuildAskMessage(entries, policies, "how does auth work?")

	assert.Contains(t, msg, "auth uses interface mocking")
	assert.Contains(t, msg, "make test-unit")
	assert.Contains(t, msg, "Fixed auth bug")
	assert.Contains(t, msg, "prefer explicit error handling")
	assert.Contains(t, msg, "how does auth work?")
}

func TestBuildAskMessage_Empty(t *testing.T) {
	msg := memory.BuildAskMessage(nil, nil, "how does auth work?")
	assert.Contains(t, msg, "how does auth work?")
	assert.Contains(t, msg, "no memory") // should indicate empty
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/memory/... -v -run TestBuildAskMessage
```

Expected: FAIL — `BuildAskMessage` undefined.

- [ ] **Step 3: Implement ask.go**

Create `internal/memory/ask.go`:

```go
package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/sidecar/internal/store"
)

const askLimit = 8

const askSystemPrompt = `You are a knowledgeable assistant for a software project. You have access to
the project's accumulated memory — facts, patterns, and history extracted from real engineering work.

Answer the developer's question using ONLY the information provided in the memory below.
Be concise, direct, and accurate. If the memory doesn't contain enough information to
answer confidently, say so explicitly rather than guessing.`

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
) (string, error) {
	// Embed the query.
	embeddings, err := provider.Embed(ctx, []string{query}, "query")
	if err != nil {
		return "", fmt.Errorf("embedding query: %w", err)
	}
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return "", fmt.Errorf("embedding returned empty result")
	}

	// Search all three memory categories (ask includes episodic unlike Retrieve).
	results, err := db.SearchMemory(ctx, workspace.ID,
		[]string{"semantic", "procedural", "episodic"}, embeddings[0], askLimit)
	if err != nil {
		return "", fmt.Errorf("searching memory: %w", err)
	}

	policies, err := db.GetPolicies(ctx, workspace.ID)
	if err != nil {
		return "", fmt.Errorf("loading policies: %w", err)
	}

	msg := BuildAskMessage(results, policies, query)

	// Synthesise answer with a single Haiku turn.
	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: llmProvider,
			Tools:    tool.NewRegistry(),
			Session:  session.NewSession("ask-"+uuid.New().String(), "main"),
		},
		runtime.AgentSpec{
			ID:           "ask",
			Name:         "Ask",
			Model:        model,
			SystemPrompt: askSystemPrompt,
			MaxTurns:     1,
		},
	)
	if err != nil {
		return "", fmt.Errorf("building ask runtime: %w", err)
	}
	defer rt.Close()

	events, err := rt.Run(ctx, msg, nil)
	if err != nil {
		return "", fmt.Errorf("ask run: %w", err)
	}

	var sb strings.Builder
	for ev := range events {
		if ev.Type == runtime.EventTextDelta {
			sb.WriteString(ev.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// BuildAskMessage formats the memory context and question for the synthesis agent.
// Exported for testing.
func BuildAskMessage(results []*store.MemorySearchResult, policies []string, query string) string {
	var sb strings.Builder

	if len(results) == 0 && len(policies) == 0 {
		sb.WriteString("Memory: no memory available for this workspace.\n\n")
		sb.WriteString("Question: " + query)
		return sb.String()
	}

	sb.WriteString("Memory:\n")

	// Group by category, include all above threshold.
	var semantic, procedural, episodic []string
	for _, r := range results {
		if r.Similarity < similarityThreshold {
			continue
		}
		switch r.Category {
		case "semantic":
			semantic = append(semantic, r.Content)
		case "procedural":
			procedural = append(procedural, r.Content)
		case "episodic":
			episodic = append(episodic, r.Content)
		}
	}

	if len(semantic) > 0 {
		sb.WriteString("\nArchitecture:\n")
		for _, s := range semantic {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(procedural) > 0 {
		sb.WriteString("\nWorkflows:\n")
		for _, s := range procedural {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(episodic) > 0 {
		sb.WriteString("\nHistory:\n")
		for _, s := range episodic {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(policies) > 0 {
		sb.WriteString("\nPolicies:\n")
		for _, p := range policies {
			sb.WriteString("- " + p + "\n")
		}
	}

	sb.WriteString("\nQuestion: " + query)
	return sb.String()
}
```

- [ ] **Step 4: Run all memory tests**

```bash
go test ./internal/memory/... -v
```

Expected: all 14 tests pass (12 existing + 2 new).

- [ ] **Step 5: Commit**

```bash
git add internal/memory/ask.go internal/memory/ask_test.go
git commit -m "feat: Ask() — retrieves memory and synthesises answer via Haiku"
```

---

### Task 3: `sidecar ask` CLI Command

**Files:**
- Create: `internal/cli/ask.go`

Note: `ask_test.go` is created in Task 4 after `askCmd` is registered in root — the test calls `root.Execute()` with `["ask", ...]` which requires the command to be present.

- [ ] **Step 1: Confirm ask.go does not yet exist**

```bash
ls /Users/sausheong/projects/sidecar/internal/cli/ask.go 2>&1
```

Expected: `No such file or directory`

- [ ] **Step 3: Implement ask.go**

Create `internal/cli/ask.go`:

```go
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/sausheong/harness/providers/anthropic"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/store"
)

func askCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask a question about the codebase using workspace memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			question := args[0]

			repoPath := repoFlag
			if repoPath == "" {
				repoPath = "."
			}
			abs, err := filepath.Abs(repoPath)
			if err != nil {
				return err
			}

			dbURL := os.Getenv("SIDECAR_DB_URL")
			if dbURL == "" {
				return fmt.Errorf("SIDECAR_DB_URL environment variable is required")
			}

			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				return fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
			}

			ctx := context.Background()
			db, err := store.Connect(ctx, dbURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()

			cfg, err := config.Load(filepath.Join(abs, "sidecar.yaml"))
			if err != nil {
				cfg = &config.Config{}
			}

			embeddingProvider := buildEmbeddingProvider(cfg)
			if embeddingProvider == nil {
				return fmt.Errorf("embedding not configured; add an 'embedding' section to sidecar.yaml")
			}

			ws, err := db.GetWorkspaceByPath(ctx, abs)
			if err != nil {
				return fmt.Errorf("workspace not found at %s — run 'sidecar attach' first", abs)
			}

			models := loop.ResolveModels(cfg)
			llmProvider := anthropic.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), "")

			answer, err := memory.Ask(ctx, embeddingProvider, llmProvider, models.Triage, db, ws, question)
			if err != nil {
				return fmt.Errorf("asking: %w", err)
			}

			fmt.Println(answer)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path to the target repository (default: .)")
	return cmd
}
```

- [ ] **Step 4: Verify build**

```bash
go build ./...
```

Expected: no errors. (`askCmd` is not yet wired into root — that happens in Task 4 — but unused functions are not a compile error in Go.)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ask.go
git commit -m "feat: sidecar ask command — query workspace memory"
```

---

### Task 4: Wire `askCmd` into Root + Tests + Final Verification

**Files:**
- Modify: `internal/cli/root.go`
- Create: `internal/cli/ask_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/ask_test.go`:

```go
package cli_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestAskCmd_RequiresDBURL(t *testing.T) {
	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"ask", "how does auth work?"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}

func TestAskCmd_RequiresAnthropicKey(t *testing.T) {
	t.Setenv("SIDECAR_DB_URL", "postgres://localhost/sidecar")
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"ask", "how does auth work?"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ANTHROPIC_API_KEY")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/cli/... -v -run TestAskCmd
```

Expected: FAIL — cobra returns "unknown command 'ask'" because it's not registered yet.

- [ ] **Step 3: Add askCmd to root**

Read `internal/cli/root.go`. Replace the `AddCommand` line:

```go
root.AddCommand(attachCmd(), taskCmd(), statusCmd(), askCmd())
```

- [ ] **Step 4: Run all CLI tests**

```bash
go test ./internal/cli/... -v
```

Expected: all 8 CLI tests pass (6 existing + 2 new `TestAskCmd_*` tests).

- [ ] **Step 5: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 6: Build and verify the binary**

```bash
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar --help
/tmp/sidecar ask --help
```

Expected output from `ask --help`:
```
Ask a question about the codebase using workspace memory

Usage:
  sidecar ask <question> [flags]

Flags:
      --repo string   path to the target repository (default: .)
```

- [ ] **Step 7: Commit**

```bash
git add internal/cli/root.go internal/cli/ask_test.go
git commit -m "feat: register sidecar ask in root command"
```

---

## Verification

After all tasks complete:

```bash
go test ./... 2>&1 | grep -v "^?"
```

Expected: all packages pass.

To exercise `sidecar ask` end-to-end (requires a workspace with memory):

```bash
export SIDECAR_DB_URL="postgres://localhost/sidecar"
export ANTHROPIC_API_KEY="..."
export OPENAI_API_KEY="..."

# After running some tasks to accumulate memory:
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar ask "how does auth work?" --repo /path/to/workspace
```

Expected: a synthesised natural-language answer drawn from memory entries, or "no memory found" if the workspace has no entries yet.
