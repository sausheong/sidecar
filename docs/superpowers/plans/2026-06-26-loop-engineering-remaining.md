# Remaining Loop-Engineering Parts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the three remaining loop-engineering parts to sidecar: skills (intent debt), daily token budget caps (token blowout), and an end-to-end gate-routing integration test.

**Architecture:** Skills wire harness's `tool/skills/disk` store into the coding runtime via `RuntimeDeps.Skills` (additive — `BuildSystemPrompt` stays). Budget caps record per-run token usage from `AgentEvent.Usage` as `usage` task-events, summed per workspace per UTC day and checked before triage. A scripted `llm.LLMProvider` drives an integration test proving the evaluator gate routes REJECT/PASS/error correctly.

**Tech Stack:** Go 1.25, harness (`runtime`, `llm`, `llm/llmtest`, `tool/skills/disk`), pgx, testify, git CLI.

## Global Constraints

- Module `github.com/sausheong/sidecar`; harness via local replace `../harness`.
- Test style: external `_test` packages, testify `require`/`assert`. Hermetic tests in default suite; DB tests use `//go:build integration` + skip when `SIDECAR_TEST_DB_URL` unset (mirror `internal/store/store_test.go`).
- Gates per task: `go build ./...` and `go vet ./...` clean; named hermetic tests pass. Integration tests gated behind the build tag are verified by `go build -tags integration ./...` compiling (running them needs a DB).
- Skills are **additive**: a repo without a skills dir behaves exactly as today (nil provider).
- Budget **fails open** (metering error → allow run); evaluator still **fails closed**.
- Budget `daily_tokens: 0` = unlimited (default) → zero behavior change unless opted in.
- Commit messages end with `Co-Authored-By: Claude <noreply@anthropic.com>`.

---

### Task 1: Config — skills dir + daily token budget

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `Config.Skills SkillsConfig` (yaml `skills`); `type SkillsConfig struct { Dir string yaml:"dir" }`.
  - `func (c *Config) SkillsDir() string` → `c.Skills.Dir` or `".sidecar/skills"` when empty.
  - `Config.Budget BudgetConfig` (yaml `budget`); `type BudgetConfig struct { DailyTokens int yaml:"daily_tokens" }`.
  - `func (c *Config) DailyTokenBudget() int` → `c.Budget.DailyTokens` (0 = unlimited).

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go`:

```go
func TestSkillsDir_DefaultWhenEmpty(t *testing.T) {
	cfg := &config.Config{}
	assert.Equal(t, ".sidecar/skills", cfg.SkillsDir())
}

func TestSkillsDir_RespectsConfigured(t *testing.T) {
	cfg := &config.Config{Skills: config.SkillsConfig{Dir: "ops/skills"}}
	assert.Equal(t, "ops/skills", cfg.SkillsDir())
}

func TestDailyTokenBudget_DefaultsZero(t *testing.T) {
	cfg := &config.Config{}
	assert.Equal(t, 0, cfg.DailyTokenBudget())
}

func TestDailyTokenBudget_RespectsConfigured(t *testing.T) {
	cfg := &config.Config{Budget: config.BudgetConfig{DailyTokens: 500000}}
	assert.Equal(t, 500000, cfg.DailyTokenBudget())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestSkillsDir|TestDailyTokenBudget' -v`
Expected: FAIL — `Skills`/`SkillsConfig`/`SkillsDir`/`Budget`/`BudgetConfig`/`DailyTokenBudget` undefined.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, add two fields to the `Config` struct (after `Verification`):

```go
	Skills        SkillsConfig         `yaml:"skills"`
	Budget        BudgetConfig         `yaml:"budget"`
```

Add the types and accessors (near `VerificationConfig`):

```go
// SkillsConfig points the loop at a directory of SKILL.md files in the target repo.
type SkillsConfig struct {
	Dir string `yaml:"dir"`
}

// SkillsDir returns the configured skills directory (relative to the repo root),
// defaulting to ".sidecar/skills".
func (c *Config) SkillsDir() string {
	if c.Skills.Dir == "" {
		return ".sidecar/skills"
	}
	return c.Skills.Dir
}

// BudgetConfig caps autonomous spend.
type BudgetConfig struct {
	// DailyTokens is the per-workspace per-UTC-day token ceiling
	// (input+output). 0 means unlimited.
	DailyTokens int `yaml:"daily_tokens"`
}

// DailyTokenBudget returns the daily token ceiling; 0 means unlimited.
func (c *Config) DailyTokenBudget() int {
	return c.Budget.DailyTokens
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): skills.dir and budget.daily_tokens

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: Store — sum workspace tokens since a time

**Files:**
- Modify: `internal/store/task.go`
- Test: `internal/store/store_test.go` (integration-tagged — already `//go:build integration`)

**Interfaces:**
- Consumes: `task_events` rows of `type='usage'` with `payload->>'total'` numeric, joined to `tasks` for workspace scoping.
- Produces: `func (db *DB) SumWorkspaceTokensSince(ctx context.Context, workspaceID uuid.UUID, since time.Time) (int, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go` (it is already `//go:build integration`; ensure `time` is imported):

```go
func TestSumWorkspaceTokensSince(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	ws := &store.Workspace{Name: "sumtok", Path: "/tmp/sumtok", ConfigHash: "h"}
	require.NoError(t, db.UpsertWorkspace(ctx, ws))

	task := &store.Task{WorkspaceID: ws.ID, SignalType: "git.commit", Summary: "x"}
	require.NoError(t, db.CreateTask(ctx, task))

	require.NoError(t, db.AppendTaskEvent(ctx, task.ID, "usage", map[string]any{"total": 1000}))
	require.NoError(t, db.AppendTaskEvent(ctx, task.ID, "usage", map[string]any{"total": 500}))
	// Non-usage events must be ignored.
	require.NoError(t, db.AppendTaskEvent(ctx, task.ID, "triage", map[string]any{"total": 9999}))

	sum, err := db.SumWorkspaceTokensSince(ctx, ws.ID, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1500, sum)

	// A future "since" excludes everything.
	sum, err = db.SumWorkspaceTokensSince(ctx, ws.ID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 0, sum)
}
```

Note: confirm the helper used by other tests to get a `*store.DB` (e.g. `testDB(t)` or inline `store.Connect`). If the existing tests use a different helper name, match it. If they connect inline, replace `testDB(t)` with the same inline `store.Connect(ctx, dbURL(t))` + `Migrate` the other tests use.

- [ ] **Step 2: Run test to verify it fails (compile)**

Run: `go test -tags integration ./internal/store/ -run TestSumWorkspaceTokensSince -v`
Expected: FAIL — `SumWorkspaceTokensSince` undefined (or skip if `SIDECAR_TEST_DB_URL` unset; in that case verify compile with `go test -tags integration -run xxx ./internal/store/` which still compiles the file).

- [ ] **Step 3: Implement**

Add to `internal/store/task.go` (ensure `time` is imported):

```go
// SumWorkspaceTokensSince returns the total tokens recorded in "usage"
// task_events for the workspace since the given time. Used by the daily
// budget cap. Returns 0 when there are no matching events.
func (db *DB) SumWorkspaceTokensSince(ctx context.Context, workspaceID uuid.UUID, since time.Time) (int, error) {
	const q = `
		SELECT COALESCE(SUM((te.payload->>'total')::bigint), 0)
		FROM task_events te
		JOIN tasks t ON t.id = te.task_id
		WHERE t.workspace_id = $1
		  AND te.type = 'usage'
		  AND te.created_at >= $2`
	var sum int64
	if err := db.pool.QueryRow(ctx, q, workspaceID, since).Scan(&sum); err != nil {
		return 0, fmt.Errorf("summing workspace tokens: %w", err)
	}
	return int(sum), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run (requires `SIDECAR_TEST_DB_URL`): `go test -tags integration ./internal/store/ -run TestSumWorkspaceTokensSince -v`
Expected: PASS. If no DB available, instead confirm it compiles: `go vet -tags integration ./internal/store/`.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): SumWorkspaceTokensSince for daily budget accounting

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: Notify — budget-exceeded event

**Files:**
- Modify: `internal/notify/notify.go`
- Test: `internal/notify/notify_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `notify.EventBudgetExceeded Event = "budget_exceeded"`.

- [ ] **Step 1: Write the failing test**

Add to `internal/notify/notify_test.go`:

```go
func TestEventBudgetExceededConstant(t *testing.T) {
	assert.Equal(t, notify.Event("budget_exceeded"), notify.EventBudgetExceeded)
}
```

(Confirm `notify_test.go` imports `notify` and testify `assert`; add if missing.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notify/ -run TestEventBudgetExceededConstant -v`
Expected: FAIL — `EventBudgetExceeded` undefined.

- [ ] **Step 3: Implement**

In `internal/notify/notify.go`, add to the `const` Event block:

```go
	EventBudgetExceeded Event = "budget_exceeded" // daily token budget reached; run skipped
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notify/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notify/
git commit -m "feat(notify): add budget_exceeded event

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: Loop — usage recording helper

**Files:**
- Modify: `internal/loop/loop.go`
- Test: `internal/loop/loop_test.go`

**Interfaces:**
- Consumes: harness `runtime.AgentEvent` (`ev.Type == runtime.EventDone`, `ev.Usage *llm.Usage` with `.InputTokens`, `.OutputTokens`).
- Produces:
  - `type usageTotals struct { Input, Output int }` with method `Total() int` returning `Input+Output`.
  - `func accumulateUsage(dst *usageTotals, ev runtime.AgentEvent)` — when `ev.Type == runtime.EventDone && ev.Usage != nil`, adds input/output to dst.

- [ ] **Step 1: Write the failing test**

Add to `internal/loop/loop_test.go` (add imports `github.com/sausheong/harness/runtime` and `github.com/sausheong/harness/llm`):

```go
func TestAccumulateUsage(t *testing.T) {
	var u loop.UsageTotals
	loop.AccumulateUsage(&u, runtime.AgentEvent{Type: runtime.EventDone, Usage: &llm.Usage{InputTokens: 100, OutputTokens: 40}})
	loop.AccumulateUsage(&u, runtime.AgentEvent{Type: runtime.EventDone, Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}})
	// Non-done / nil usage events are ignored.
	loop.AccumulateUsage(&u, runtime.AgentEvent{Type: runtime.EventTextDelta})
	loop.AccumulateUsage(&u, runtime.AgentEvent{Type: runtime.EventDone, Usage: nil})

	assert.Equal(t, 110, u.Input)
	assert.Equal(t, 45, u.Output)
	assert.Equal(t, 155, u.Total())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/loop/ -run TestAccumulateUsage -v`
Expected: FAIL — `UsageTotals`/`AccumulateUsage` undefined.

- [ ] **Step 3: Implement**

Add to `internal/loop/loop.go` (exported for testing):

```go
// UsageTotals accumulates token usage across an agent run's events.
type UsageTotals struct {
	Input  int
	Output int
}

// Total returns the combined input+output tokens.
func (u UsageTotals) Total() int { return u.Input + u.Output }

// AccumulateUsage adds an event's reported usage to dst. Only EventDone
// events carrying a non-nil Usage contribute; others are ignored.
func AccumulateUsage(dst *UsageTotals, ev runtime.AgentEvent) {
	if ev.Type == runtime.EventDone && ev.Usage != nil {
		dst.Input += ev.Usage.InputTokens
		dst.Output += ev.Usage.OutputTokens
	}
}
```

Add `"github.com/sausheong/harness/llm"` to imports if not already present (it is — `llm.LLMProvider` is used).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/loop/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/loop/
git commit -m "feat(loop): UsageTotals + AccumulateUsage for token accounting

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: Loop — skills provider builder

**Files:**
- Create: `internal/loop/skills.go`
- Test: `internal/loop/skills_test.go`

**Interfaces:**
- Consumes: `config.Config.SkillsDir()`, harness `tool/skills/disk.NewStore(root).AsSkillProvider()`, `runtime.SkillProvider`.
- Produces: `func buildSkillsProvider(repoPath string, cfg *config.Config) runtime.SkillProvider` — returns a provider when `<repoPath>/<SkillsDir>` exists as a directory, else nil. Exported wrapper `BuildSkillsProvider` for tests.

- [ ] **Step 1: Write the failing test**

Create `internal/loop/skills_test.go`:

```go
package loop_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSkillsProvider_NilWhenAbsent(t *testing.T) {
	repo := t.TempDir()
	cfg := &config.Config{}
	assert.Nil(t, loop.BuildSkillsProvider(repo, cfg))
}

func TestBuildSkillsProvider_NonNilWhenPresent(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".sidecar", "skills"), 0o755))
	cfg := &config.Config{}
	assert.NotNil(t, loop.BuildSkillsProvider(repo, cfg))
}

func TestBuildSkillsProvider_RespectsConfiguredDir(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "ops", "skills"), 0o755))
	cfg := &config.Config{Skills: config.SkillsConfig{Dir: "ops/skills"}}
	assert.NotNil(t, loop.BuildSkillsProvider(repo, cfg))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/loop/ -run TestBuildSkillsProvider -v`
Expected: FAIL — `BuildSkillsProvider` undefined.

- [ ] **Step 3: Implement**

Create `internal/loop/skills.go`:

```go
package loop

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/tool/skills/disk"
	"github.com/sausheong/sidecar/internal/config"
)

// BuildSkillsProvider returns a harness SkillProvider backed by the target
// repo's skills directory (config.SkillsDir, default ".sidecar/skills"),
// or nil when that directory does not exist. A nil provider means the
// coding runtime gets no skills index and no load_skill tool — identical
// to the prior behavior, so repos without skills are unaffected.
func BuildSkillsProvider(repoPath string, cfg *config.Config) runtime.SkillProvider {
	dir := filepath.Join(repoPath, cfg.SkillsDir())
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	return disk.NewStore(dir).AsSkillProvider()
}

// buildSkillsProvider is the unexported call site used by Loop.New.
func buildSkillsProvider(repoPath string, cfg *config.Config) runtime.SkillProvider {
	p := BuildSkillsProvider(repoPath, cfg)
	if p != nil {
		slog.Info("sidecar: skills enabled", "dir", cfg.SkillsDir())
	}
	return p
}
```

Note: if `disk.NewStore(...).AsSkillProvider()` does not directly satisfy `runtime.SkillProvider`, check the concrete return type (`*disk.SkillProviderAdapter`) — it implements `FormatIndex`/`Get` which is the `runtime.SkillProvider` interface. If the compiler reports a mismatch, read `../harness/runtime/types.go` `SkillProvider` and adapt (the adapter is designed to satisfy it).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/loop/ -run TestBuildSkillsProvider -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 5: Commit**

```bash
git add internal/loop/skills.go internal/loop/skills_test.go
git commit -m "feat(loop): build skills provider from target repo's skills dir

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: Loop — wire skills, usage recording, and budget gate into Run

**Files:**
- Modify: `internal/loop/loop.go`
- Test: build + hermetic suite (Run wiring verified end-to-end by Task 7's integration test)

**Interfaces:**
- Consumes: `buildSkillsProvider` (Task 5), `UsageTotals`/`AccumulateUsage` (Task 4), `cfg.DailyTokenBudget()` (Task 1), `db.SumWorkspaceTokensSince` (Task 2), `notify.EventBudgetExceeded` (Task 3).
- Produces: updated `Loop` struct (+`skills runtime.SkillProvider`), updated `Run`.

- [ ] **Step 1: Add skills field to Loop + New**

In `internal/loop/loop.go`, add to the `Loop` struct:

```go
	skills     runtime.SkillProvider    // nil when no skills dir present
```

In `New`, set it before the `return`:

```go
	skills := buildSkillsProvider(repoPath, cfg)
```

and add `skills: skills,` to the returned `&Loop{...}`.

- [ ] **Step 2: Add the budget gate (before triage)**

In `Run`, immediately after the `CreateTask` block and BEFORE the `// ── Triage ──` section, insert:

```go
	// ── Budget gate ──────────────────────────────────────────────────────────
	// Checked before any LLM spend. Fails OPEN: a metering error allows the run
	// (the cap is a cost guard, not a safety gate — unlike the evaluator).
	if budget := l.cfg.DailyTokenBudget(); budget > 0 {
		startOfDay := time.Now().UTC().Truncate(24 * time.Hour)
		spent, bErr := l.db.SumWorkspaceTokensSince(ctx, l.workspace.ID, startOfDay)
		if bErr != nil {
			slog.Warn("budget check failed; allowing run (fail open)", "err", bErr, "task", task.ID)
		} else if spent >= budget {
			slog.Info("sidecar: daily token budget reached; skipping", "spent", spent, "budget", budget, "task", task.ID)
			_ = l.db.AppendTaskEvent(ctx, task.ID, "budget_exceeded", map[string]any{"spent": spent, "budget": budget})
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusSkipped)
			l.dispatcher.Fire(ctx, notify.EventBudgetExceeded, sig, task)
			return nil
		}
	}
```

Ensure `"time"` is imported (it is — used by runReview).

- [ ] **Step 3: Pass skills to the coding runtime + record coding usage**

In `Run`, change the coding runtime's `RuntimeDeps{}` (around line 248) to:

```go
		runtime.RuntimeDeps{Skills: l.skills},
```

Then change the coding event loop (around lines 270-277) to also accumulate usage and write a usage event after the loop. Replace:

```go
	var agentErr error
	var textBuf strings.Builder
	for ev := range events {
		if ev.Type == runtime.EventError {
			agentErr = ev.Error
		}
		if ev.Type == runtime.EventTextDelta {
			textBuf.WriteString(ev.Text)
		}
	}
```

with:

```go
	var agentErr error
	var textBuf strings.Builder
	var codingUsage UsageTotals
	for ev := range events {
		if ev.Type == runtime.EventError {
			agentErr = ev.Error
		}
		if ev.Type == runtime.EventTextDelta {
			textBuf.WriteString(ev.Text)
		}
		AccumulateUsage(&codingUsage, ev)
	}
	if codingUsage.Total() > 0 {
		_ = l.db.AppendTaskEvent(ctx, task.ID, "usage", map[string]any{
			"input": codingUsage.Input, "output": codingUsage.Output,
			"total": codingUsage.Total(), "model": models.Coding, "role": "coding",
		})
	}
```

- [ ] **Step 4: Verify build, vet, hermetic tests**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build/vet clean; all hermetic packages `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/loop/loop.go
git commit -m "feat(loop): wire skills into coding runtime, record usage, enforce daily budget

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: Integration test — gate routing + budget enforcement

**Files:**
- Create: `internal/loop/run_integration_test.go`

**Interfaces:**
- Consumes: `loop.New`, `loop.Loop.Run`, `store` (real DB), harness `llm`/`llmtest`/`runtime`, `adapter.Signal`.
- Produces: a scripted `llm.LLMProvider` and four integration cases.

Note on the scripted provider: harness `llm.LLMProvider` requires `ChatStream`, `Models`, `NormalizeToolSchema`. Embed `llmtest.Base` for the latter two. `ChatStream` must return a channel of `llm.ChatEvent`. Read `../harness/llm/provider.go` for `ChatEvent`/`ChatRequest`/`Usage` shapes and `../harness/llm/llmtest/llmtest.go` for the `Stub` pattern, and `../harness/runtime/agent_test.go` for how a scripted provider drives a full agent turn (text + tool calls + a terminal event carrying Usage). Model the responses on those.

- [ ] **Step 1: Write the integration test scaffold + provider**

Create `internal/loop/run_integration_test.go` beginning with the build tag and a scripted provider. The provider routes by inspecting the request's system prompt / model to decide which canned turn to emit:
- triage model → a JSON classification text turn,
- evaluator (system prompt contains "adversarial code reviewer") → a JSON verdict text turn (pass toggled by a field on the provider),
- otherwise (coding) → a tool call writing a file, then a stop.

```go
//go:build integration

package loop_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/llm/llmtest"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dbURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("SIDECAR_TEST_DB_URL")
	if u == "" {
		t.Skip("SIDECAR_TEST_DB_URL not set")
	}
	return u
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "t@t.com"},
		{"git", "-C", dir, "config", "user.name", "T"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}
```

IMPORTANT: the scripted-provider implementation must match harness's real `ChatEvent`/`ChatRequest` types. Before writing it, READ `../harness/llm/provider.go` (ChatRequest fields: messages, system, tools, model; ChatEvent variants for text, tool-use, done+usage) and `../harness/llm/llmtest/llmtest.go` and `../harness/runtime/agent_test.go` for a concrete scripted provider that emits a tool call and a terminal usage event. Implement `scriptedProvider` accordingly: a struct embedding `llmtest.Base` with an `evalPass bool` and `evalErr bool` field, whose `ChatStream` switches on `req` to emit the right canned events (use the file-write tool name registered by sidecar's loop, `write_file`, with JSON args writing a file under the workspace).

- [ ] **Step 2: Write the four cases**

Append:

```go
func newLoop(t *testing.T, repo string, p llm.LLMProvider, cfg *config.Config) (*loop.Loop, *store.DB, *store.Workspace) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Connect(ctx, dbURL(t))
	require.NoError(t, err)
	require.NoError(t, store.Migrate(ctx, db))
	ws := &store.Workspace{Name: "itest", Path: repo, ConfigHash: "h"}
	require.NoError(t, db.UpsertWorkspace(ctx, ws))
	l := loop.New(db, ws, cfg, repo, nil)
	loop.SetProviderForTest(l, p) // inject scripted provider
	return l, db, ws
}

func bugFixCfg() *config.Config {
	tr := true
	return &config.Config{
		Autonomy:     config.AutonomyPolicy{BugFixes: "auto-commit"},
		Verification: config.VerificationConfig{Enabled: &tr},
	}
}

func gitCommitSignal() adapter.Signal {
	return adapter.Signal{Type: adapter.SignalGitCommit, Source: "git", Payload: map[string]any{"hash": "abc123"}}
}

func lastStatus(t *testing.T, db *store.DB, ws *store.Workspace) string {
	tasks, err := db.ListTasks(context.Background(), ws.ID, 1)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	return tasks[0].Status
}

func TestRun_PassCommits(t *testing.T) {
	repo := initRepo(t)
	l, db, ws := newLoop(t, repo, &scriptedProvider{evalPass: true}, bugFixCfg())
	require.NoError(t, l.Run(context.Background(), gitCommitSignal()))
	assert.Equal(t, loop.StatusCompleted, lastStatus(t, db, ws))
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "sidecar/*").Output()
	assert.NotEmpty(t, strings.TrimSpace(string(out)), "a sidecar branch should exist")
}

func TestRun_RejectSuggests(t *testing.T) {
	repo := initRepo(t)
	l, db, ws := newLoop(t, repo, &scriptedProvider{evalPass: false}, bugFixCfg())
	require.NoError(t, l.Run(context.Background(), gitCommitSignal()))
	assert.Equal(t, loop.StatusSuggested, lastStatus(t, db, ws))
}

func TestRun_EvaluatorErrorFailsClosed(t *testing.T) {
	repo := initRepo(t)
	l, db, ws := newLoop(t, repo, &scriptedProvider{evalErr: true}, bugFixCfg())
	require.NoError(t, l.Run(context.Background(), gitCommitSignal()))
	assert.Equal(t, loop.StatusSuggested, lastStatus(t, db, ws))
}

func TestRun_BudgetExceededSkips(t *testing.T) {
	repo := initRepo(t)
	cfg := bugFixCfg()
	cfg.Budget = config.BudgetConfig{DailyTokens: 100}
	l, db, ws := newLoop(t, repo, &scriptedProvider{evalPass: true}, cfg)
	// Pre-seed usage over budget on a throwaway task.
	seed := &store.Task{WorkspaceID: ws.ID, SignalType: "git.commit", Summary: "seed"}
	require.NoError(t, db.CreateTask(context.Background(), seed))
	require.NoError(t, db.AppendTaskEvent(context.Background(), seed.ID, "usage", map[string]any{"total": 1000}))
	require.NoError(t, l.Run(context.Background(), gitCommitSignal()))
	assert.Equal(t, loop.StatusSkipped, lastStatus(t, db, ws))
}
```

- [ ] **Step 3: Add the test-only provider injector**

The loop currently builds its provider internally in `New`. Add a test-only setter in a NON-test file guarded so it stays internal. Create `internal/loop/testhooks.go`:

```go
package loop

import "github.com/sausheong/harness/llm"

// SetProviderForTest overrides the LLM provider. Test-only seam; not used in
// production. Kept in a normal file (not _test.go) so integration tests in
// package loop_test can call it.
func SetProviderForTest(l *Loop, p llm.LLMProvider) { l.provider = p }
```

- [ ] **Step 4: Verify it compiles under the integration tag**

Run: `go vet -tags integration ./internal/loop/`
Expected: clean (no undefined symbols). If `SIDECAR_TEST_DB_URL` is set, run `go test -tags integration ./internal/loop/ -run TestRun -v` and expect PASS; otherwise the cases skip.

- [ ] **Step 5: Verify the default suite is unaffected**

Run: `go build ./... && go test ./...`
Expected: build clean; hermetic suite green (the integration file is excluded without the tag).

- [ ] **Step 6: Commit**

```bash
git add internal/loop/run_integration_test.go internal/loop/testhooks.go
git commit -m "test(loop): integration test for evaluator gate routing and budget gate

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: Docs — sidecar.yaml + CLAUDE.md

**Files:**
- Modify: `sidecar.yaml`, `CLAUDE.md`

**Interfaces:**
- Consumes: nothing.
- Produces: documentation for skills, budget, and the integration test.

- [ ] **Step 1: Update sidecar.yaml**

Add to `sidecar.yaml`:

```yaml
skills:
  dir: .sidecar/skills   # SKILL.md files in the target repo; loaded into the coding agent

budget:
  daily_tokens: 0        # per-workspace per-UTC-day input+output token cap; 0 = unlimited
```

- [ ] **Step 2: Update CLAUDE.md**

In `CLAUDE.md`, add two bullets under "Key Design Decisions":

```markdown
- **Skills** — the coding agent loads `SKILL.md` files from the target repo's `.sidecar/skills/` (configurable via `skills.dir`) through harness's skill provider; absent dir ⇒ no change. Pays off the paper's "intent debt".
- **Budget caps** — `budget.daily_tokens` enforces a per-workspace per-UTC-day token ceiling (input+output, summed from `usage` task events) checked before triage; fails open on metering error. The paper's "cap before you ship".
```

Add an integration-test line to the Standard Go Commands section:

```markdown
go test -tags integration ./internal/loop/...               # gate-routing + budget (needs SIDECAR_TEST_DB_URL)
```

- [ ] **Step 3: Verify YAML validity + build sanity**

Run: `python3 -c "import yaml; yaml.safe_load(open('sidecar.yaml')); print('ok')" && go build ./...`
Expected: `ok`; build clean.

- [ ] **Step 4: Commit**

```bash
git add sidecar.yaml CLAUDE.md
git commit -m "docs: document skills, budget caps, and integration tests

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec coverage:** skills config+builder+wiring (T1/T5/T6), budget config+store+event+gate (T1/T2/T3/T6), usage recording (T4/T6), gate-routing+budget integration test (T7), docs (T8). All spec items mapped.
- **Type consistency:** `SkillsConfig.Dir`/`SkillsDir()`, `BudgetConfig.DailyTokens`/`DailyTokenBudget()`, `SumWorkspaceTokensSince(ctx,uuid,time)`, `EventBudgetExceeded`, `UsageTotals{Input,Output}`/`Total()`/`AccumulateUsage`, `BuildSkillsProvider`/`buildSkillsProvider`, `SetProviderForTest` are used identically across tasks.
- **Additive guarantee:** T5 returns nil when dir absent; T6 passes `RuntimeDeps{Skills: nil}` in that case → behavior identical to today. Budget default 0 → gate skipped.
- **Fail directions:** budget fails open (T6 logs + proceeds on query error); evaluator unchanged (still fails closed).
- **Known harness-API risk flagged in T5/T7:** the plan tells the implementer to verify `AsSkillProvider()` satisfies `runtime.SkillProvider` and to read real harness types before writing the scripted provider, rather than trusting the snippet blindly.
