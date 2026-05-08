# Sidecar Phase 2 — Reactive Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add GitHub CI failure detection, a Haiku triage step (act/skip + change classification), autonomy-based output routing (auto-commit / pull-request / suggest-only), and PR creation to the existing Sidecar Phase 1 runtime.

**Architecture:** Triage lives inside `Loop.Run` — a MaxTurns=1 Haiku agent call returns structured JSON that determines whether to act and what autonomy level to apply. The GitHub CI adapter polls the GitHub Actions API on a configurable interval. PR creation tries `gh` CLI first then falls back to the GitHub REST API. The daemon and attach command remain unchanged except for wiring the new adapter.

**Tech Stack:** Go 1.25+, `github.com/sausheong/harness` (already in go.mod), `net/http` (stdlib — no new dependencies for GitHub API calls), existing pgx/v5, cobra.

---

## File Map

```
internal/
  adapter/
    adapter.go              MODIFY — add SignalCIFailure constant
  adapter/githubci/
    githubci.go             CREATE — GitHub Actions poller
    githubci_test.go        CREATE
  config/
    config.go               MODIFY — add Repo, Token, PollInterval to SignalConfig
    config_test.go          MODIFY — extend TestLoad
  store/
    task.go                 MODIFY — add AppendTaskEvent method
    store_test.go           MODIFY — add TestTaskEvent_Append integration test
  triage/
    triage.go               CREATE — TriageResult, pure functions, Triage()
    triage_test.go          CREATE
  output/
    pr.go                   CREATE — PRCreator (gh CLI + API fallback)
    pr_test.go              CREATE
  loop/
    loop.go                 MODIFY — triage step, autonomy routing, new statuses
    loop_test.go            MODIFY — add CI failure prompt test, status constant tests
  cli/
    attach.go               MODIFY — add github-ci to buildAdapters()
sidecar.yaml                MODIFY — add github-ci example signal
```

## Environment Variables (Phase 2 additions)

| Variable | Required | Description |
|----------|----------|-------------|
| `GITHUB_TOKEN` | yes, for CI adapter + PR | Personal access token with `repo` scope |

---

### Task 1: Add `SignalCIFailure` Constant

**Files:**
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/adapter/adapter_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/adapter/adapter_test.go` inside `TestSignalTypes`:

```go
func TestSignalTypes(t *testing.T) {
	assert.Equal(t, adapter.SignalType("git.commit"), adapter.SignalGitCommit)
	assert.Equal(t, adapter.SignalType("schedule.tick"), adapter.SignalScheduleTick)
	assert.Equal(t, adapter.SignalType("ondemand.task"), adapter.SignalOnDemand)
	assert.Equal(t, adapter.SignalType("ci.failure"), adapter.SignalCIFailure) // new
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/... -v
```

Expected: FAIL — `SignalCIFailure` undefined.

- [ ] **Step 3: Add the constant**

In `internal/adapter/adapter.go`, extend the const block:

```go
const (
	SignalGitCommit    SignalType = "git.commit"
	SignalScheduleTick SignalType = "schedule.tick"
	SignalOnDemand     SignalType = "ondemand.task"
	SignalCIFailure    SignalType = "ci.failure"
)
```

- [ ] **Step 4: Run the test**

```bash
go test ./internal/adapter/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/adapter.go internal/adapter/adapter_test.go
git commit -m "feat: add SignalCIFailure signal type"
```

---

### Task 2: Config Changes for `github-ci` Adapter

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add `TestLoad_GitHubCI` to `internal/config/config_test.go`:

```go
func TestLoad_GitHubCI(t *testing.T) {
	yaml := `
signals:
  - adapter: github-ci
    repo: myorg/payment-service
    token: $GITHUB_TOKEN
    poll_interval: 60s
    watch: [failure]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	require.Len(t, cfg.Signals, 1)
	s := cfg.Signals[0]
	assert.Equal(t, "github-ci", s.Adapter)
	assert.Equal(t, "myorg/payment-service", s.Repo)
	assert.Equal(t, "$GITHUB_TOKEN", s.Token)
	assert.Equal(t, "60s", s.PollInterval)
	assert.Equal(t, []string{"failure"}, s.Watch)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/config/... -v -run TestLoad_GitHubCI
```

Expected: FAIL — `Repo`, `Token`, `PollInterval` fields undefined on `SignalConfig`.

- [ ] **Step 3: Add fields to SignalConfig**

In `internal/config/config.go`, update `SignalConfig`:

```go
type SignalConfig struct {
	Adapter      string   `yaml:"adapter"`
	Watch        []string `yaml:"watch"`
	Cron         string   `yaml:"cron"`
	Repo         string   `yaml:"repo"`          // owner/repo slug (github-ci adapter)
	Token        string   `yaml:"token"`         // literal or $ENV_VAR reference
	PollInterval string   `yaml:"poll_interval"` // e.g. "60s", default "60s"
}
```

- [ ] **Step 4: Run the test**

```bash
go test ./internal/config/... -v
```

Expected: all 4 tests pass.

- [ ] **Step 5: Update example sidecar.yaml**

Add a commented-out github-ci example to `sidecar.yaml` in the repo root. Replace the existing file with:

```yaml
workspace:
  name: my-service
  language: go

signals:
  - adapter: git
    watch: [push, pr]
  - adapter: schedule
    cron: "0 0 2 * * *"   # 6-field cron with seconds: nightly at 02:00
  # - adapter: github-ci
  #   repo: myorg/my-service
  #   token: $GITHUB_TOKEN
  #   poll_interval: 60s
  #   watch: [failure]

autonomy:
  dependency_updates: auto-commit
  test_fixes: auto-commit
  bug_fixes: pull-request
  refactoring: suggest-only
  schema_changes: suggest-only

models:
  planning: anthropic/claude-sonnet-4-6
  coding: anthropic/claude-sonnet-4-6
  triage: anthropic/claude-haiku-4-5

scope:
  include: [src/, tests/]
  exclude: [secrets/]
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go sidecar.yaml
git commit -m "feat: add github-ci config fields to SignalConfig"
```

---

### Task 3: `AppendTaskEvent` Store Method

**Files:**
- Modify: `internal/store/task.go`
- Modify: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `internal/store/store_test.go` (inside the `//go:build integration` file):

```go
func TestTaskEvent_Append(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "evt-svc", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	task := &store.Task{WorkspaceID: ws.ID, SignalType: "ci.failure", Summary: "test"}
	require.NoError(t, db.CreateTask(context.Background(), task))

	payload := map[string]any{"should_act": true, "change_type": "test_fix", "reason": "tests failing"}
	err = db.AppendTaskEvent(context.Background(), task.ID, "triage", payload)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v -run TestTaskEvent_Append
```

Expected: FAIL — `AppendTaskEvent` undefined.

- [ ] **Step 3: Implement AppendTaskEvent**

Add to `internal/store/task.go`:

```go
import "encoding/json" // add to existing import block

// AppendTaskEvent records a single event for a task in the task_events table.
// payload is marshaled to JSONB.
func (db *DB) AppendTaskEvent(ctx context.Context, taskID uuid.UUID, eventType string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling event payload: %w", err)
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO task_events (task_id, type, payload)
		VALUES ($1, $2, $3)`,
		taskID, eventType, data,
	)
	if err != nil {
		return fmt.Errorf("appending task event: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the integration test**

```bash
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v
```

Expected: all 5 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/store/task.go internal/store/store_test.go
git commit -m "feat: AppendTaskEvent store method for audit trail"
```

---

### Task 4: Triage Package

**Files:**
- Create: `internal/triage/triage.go`
- Create: `internal/triage/triage_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/triage/triage_test.go`:

```go
package triage_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/triage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTriageResponse_Valid(t *testing.T) {
	raw := `{"should_act": true, "change_type": "test_fix", "reason": "3 assertions failing"}`
	result, err := triage.ParseTriageResponse(raw)
	require.NoError(t, err)
	assert.True(t, result.ShouldAct)
	assert.Equal(t, "test_fix", result.ChangeType)
	assert.Equal(t, "3 assertions failing", result.Reason)
}

func TestParseTriageResponse_ShouldNotAct(t *testing.T) {
	raw := `{"should_act": false, "change_type": "unknown", "reason": "docs-only change"}`
	result, err := triage.ParseTriageResponse(raw)
	require.NoError(t, err)
	assert.False(t, result.ShouldAct)
}

func TestParseTriageResponse_Invalid(t *testing.T) {
	_, err := triage.ParseTriageResponse("not json at all")
	assert.Error(t, err)
}

func TestResolveAutonomy(t *testing.T) {
	cfg := &config.Config{
		Autonomy: config.AutonomyPolicy{
			TestFixes:         "auto-commit",
			BugFixes:          "pull-request",
			DependencyUpdates: "auto-commit",
			Refactoring:       "suggest-only",
		},
	}

	assert.Equal(t, "auto-commit", triage.ResolveAutonomy("test_fix", cfg))
	assert.Equal(t, "pull-request", triage.ResolveAutonomy("bug_fix", cfg))
	assert.Equal(t, "auto-commit", triage.ResolveAutonomy("dependency_update", cfg))
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("refactor", cfg))
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("unknown", cfg))
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("schema_change", cfg)) // unmapped → safe default
}

func TestBuildTriageMessage_CIFailure(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalCIFailure,
		Source: "github-ci",
		Payload: map[string]any{
			"workflow_name": "CI",
			"conclusion":    "failure",
			"head_sha":      "abc123",
			"html_url":      "https://github.com/org/repo/actions/runs/1",
			"repo":          "org/repo",
			"is_flake":      false,
		},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "CI")
	assert.Contains(t, msg, "abc123")
	assert.Contains(t, msg, "failure")
}

func TestBuildTriageMessage_OnDemand(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalOnDemand,
		Payload: map[string]any{"description": "fix the auth bug"},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "fix the auth bug")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/triage/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Implement triage.go**

Create `internal/triage/triage.go`:

```go
package triage

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
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
)

// TriageResult is the structured output of the triage agent.
type TriageResult struct {
	ShouldAct     bool   // false = skip this signal entirely
	ChangeType    string // "test_fix" | "bug_fix" | "dependency_update" | "refactor" | "unknown"
	AutonomyLevel string // "auto-commit" | "pull-request" | "suggest-only"
	Reason        string // one-sentence explanation
}

const triageSystemPrompt = `You are a triage agent for an autonomous software engineering system.
Analyze the incoming signal and decide:
1. Whether it warrants autonomous action (should_act: true/false)
2. If so, what type of change is needed

Valid change_type values: "test_fix", "bug_fix", "dependency_update", "refactor", "unknown"

Do NOT act (should_act: false) for:
- CI failures caused by infrastructure issues (network timeouts, disk full, runner unavailable)
- Documentation-only commits that cannot cause test failures
- Signals with insufficient information to determine a fix

Respond with ONLY valid JSON, no prose:
{"should_act": true, "change_type": "test_fix", "reason": "one sentence"}`

// Triage calls the triage model to classify a signal.
// On any failure it returns a conservative default (suggest-only) rather than dropping the signal.
func Triage(ctx context.Context, provider llm.LLMProvider, model string, sig adapter.Signal, cfg *config.Config) (TriageResult, error) {
	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: provider,
			Tools:    tool.NewRegistry(),
			Session:  session.NewSession("triage-"+uuid.New().String(), "main"),
		},
		runtime.AgentSpec{
			ID:           "triage",
			Name:         "Triage",
			Model:        model,
			SystemPrompt: triageSystemPrompt,
			MaxTurns:     1,
		},
	)
	if err != nil {
		slog.Warn("triage runtime build failed, defaulting to suggest-only", "err", err)
		return conservativeDefault(), nil
	}
	defer rt.Close()

	events, err := rt.Run(ctx, BuildTriageMessage(sig), nil)
	if err != nil {
		slog.Warn("triage run failed, defaulting to suggest-only", "err", err)
		return conservativeDefault(), nil
	}

	var sb strings.Builder
	for ev := range events {
		if ev.Type == runtime.EventTextDelta {
			sb.WriteString(ev.Text)
		}
	}

	result, err := ParseTriageResponse(sb.String())
	if err != nil {
		slog.Warn("triage response parse failed, defaulting to suggest-only", "err", err, "raw", sb.String())
		return conservativeDefault(), nil
	}

	result.AutonomyLevel = ResolveAutonomy(result.ChangeType, cfg)
	return result, nil
}

// BuildTriageMessage constructs the user-turn message sent to the triage agent.
func BuildTriageMessage(sig adapter.Signal) string {
	switch sig.Type {
	case adapter.SignalCIFailure:
		workflow, _ := sig.Payload["workflow_name"].(string)
		conclusion, _ := sig.Payload["conclusion"].(string)
		sha, _ := sig.Payload["head_sha"].(string)
		url, _ := sig.Payload["html_url"].(string)
		repo, _ := sig.Payload["repo"].(string)
		isFlake, _ := sig.Payload["is_flake"].(bool)
		return fmt.Sprintf("GitHub Actions CI failure:\nWorkflow: %s\nConclusion: %s\nCommit: %s\nURL: %s\nRepo: %s\nFlaky: %v\n\nShould this be fixed automatically?",
			workflow, conclusion, sha, url, repo, isFlake)
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		return fmt.Sprintf("New git commit: %s\nShould this commit be reviewed and fixed if it introduced issues?", hash)
	case adapter.SignalScheduleTick:
		return "Scheduled maintenance sweep. Should a proactive improvement be made to the codebase?"
	default:
		desc, _ := sig.Payload["description"].(string)
		return fmt.Sprintf("On-demand task: %s\nShould this task be executed?", desc)
	}
}

// ParseTriageResponse unmarshals the triage agent's JSON response.
func ParseTriageResponse(raw string) (TriageResult, error) {
	raw = strings.TrimSpace(raw)
	var resp struct {
		ShouldAct  bool   `json:"should_act"`
		ChangeType string `json:"change_type"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return TriageResult{}, fmt.Errorf("parsing triage JSON: %w", err)
	}
	return TriageResult{
		ShouldAct:  resp.ShouldAct,
		ChangeType: resp.ChangeType,
		Reason:     resp.Reason,
	}, nil
}

// ResolveAutonomy maps a change type to an autonomy level using the config.
// Unknown or unmapped types fall back to "suggest-only".
func ResolveAutonomy(changeType string, cfg *config.Config) string {
	var level string
	switch changeType {
	case "test_fix":
		level = cfg.Autonomy.TestFixes
	case "bug_fix":
		level = cfg.Autonomy.BugFixes
	case "dependency_update":
		level = cfg.Autonomy.DependencyUpdates
	case "refactor":
		level = cfg.Autonomy.Refactoring
	}
	if level == "" {
		return "suggest-only"
	}
	return level
}

func conservativeDefault() TriageResult {
	return TriageResult{
		ShouldAct:     true,
		ChangeType:    "unknown",
		AutonomyLevel: "suggest-only",
		Reason:        "triage failed, defaulting to suggest-only",
	}
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/triage/... -v
```

Expected: all 6 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/triage/
git commit -m "feat: triage package — classify signals with Haiku before acting"
```

---

### Task 5: GitHub CI Adapter

**Files:**
- Create: `internal/adapter/githubci/githubci.go`
- Create: `internal/adapter/githubci/githubci_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/githubci/githubci_test.go`:

```go
package githubci_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/adapter/githubci"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockRunsResponse(runs []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"workflow_runs": runs,
		})
	}
}

func TestGitHubCIAdapter_DetectsFailure(t *testing.T) {
	server := httptest.NewServer(mockRunsResponse([]map[string]any{
		{
			"id":            int64(1001),
			"name":          "CI",
			"conclusion":    "failure",
			"html_url":      "https://github.com/org/repo/actions/runs/1001",
			"head_sha":      "abc123",
			"workflow_id":   int64(42),
		},
	}))
	defer server.Close()

	a := githubci.NewWithBaseURL("org/repo", "token", 100*time.Millisecond, []string{"failure"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalCIFailure, sig.Type)
		assert.Equal(t, "github-ci", sig.Source)
		assert.Equal(t, int64(1001), sig.Payload["run_id"])
		assert.Equal(t, "CI", sig.Payload["workflow_name"])
		assert.Equal(t, "failure", sig.Payload["conclusion"])
		assert.Equal(t, "abc123", sig.Payload["head_sha"])
		assert.Equal(t, "org/repo", sig.Payload["repo"])
	case <-ctx.Done():
		t.Fatal("no signal received within timeout")
	}
}

func TestGitHubCIAdapter_NoDuplicates(t *testing.T) {
	// Run IDs 1001 and 1002 are present from the first poll.
	server := httptest.NewServer(mockRunsResponse([]map[string]any{
		{"id": int64(1001), "name": "CI", "conclusion": "failure",
			"html_url": "https://github.com/org/repo/actions/runs/1001",
			"head_sha": "abc", "workflow_id": int64(42)},
		{"id": int64(1002), "name": "CI", "conclusion": "failure",
			"html_url": "https://github.com/org/repo/actions/runs/1002",
			"head_sha": "def", "workflow_id": int64(42)},
	}))
	defer server.Close()

	a := githubci.NewWithBaseURL("org/repo", "token", 100*time.Millisecond, []string{"failure"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	// Drain first batch
	for i := 0; i < 2; i++ {
		select {
		case <-signals:
		case <-ctx.Done():
			t.Fatal("timed out waiting for initial signals")
		}
	}

	// Wait for 2 more polls — same run IDs should not fire again
	time.Sleep(350 * time.Millisecond)
	assert.Empty(t, signals, "duplicate run IDs should not re-fire")
}

func TestGitHubCIAdapter_IgnoresNonWatchedConclusion(t *testing.T) {
	server := httptest.NewServer(mockRunsResponse([]map[string]any{
		{"id": int64(2001), "name": "CI", "conclusion": "success",
			"html_url": "https://github.com/org/repo/actions/runs/2001",
			"head_sha": "xyz", "workflow_id": int64(42)},
	}))
	defer server.Close()

	a := githubci.NewWithBaseURL("org/repo", "token", 100*time.Millisecond, []string{"failure"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(350 * time.Millisecond)
	assert.Empty(t, signals, "successful runs should not fire when only 'failure' is watched")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/githubci/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Implement githubci.go**

Create `internal/adapter/githubci/githubci.go`:

```go
package githubci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
)

const defaultBaseURL = "https://api.github.com"

// GitHubCIAdapter polls GitHub Actions for failed workflow runs.
type GitHubCIAdapter struct {
	repo         string // "owner/repo"
	token        string
	pollInterval time.Duration
	watch        []string // conclusions to watch: "failure", "timed_out", etc.
	baseURL      string

	seen     map[int64]bool
	seenMu   sync.Mutex
	stopOnce sync.Once
	stopCh   chan struct{}
	client   *http.Client
}

// New creates a GitHubCIAdapter with the GitHub API base URL.
func New(repo, token string, pollInterval time.Duration, watch []string) *GitHubCIAdapter {
	return NewWithBaseURL(repo, token, pollInterval, watch, defaultBaseURL)
}

// NewWithBaseURL creates a GitHubCIAdapter with a custom base URL (used in tests).
func NewWithBaseURL(repo, token string, pollInterval time.Duration, watch []string, baseURL string) *GitHubCIAdapter {
	return &GitHubCIAdapter{
		repo:         repo,
		token:        token,
		pollInterval: pollInterval,
		watch:        watch,
		baseURL:      baseURL,
		seen:         make(map[int64]bool),
		stopCh:       make(chan struct{}),
		client:       &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *GitHubCIAdapter) Name() string { return "github-ci" }

func (a *GitHubCIAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	go func() {
		ticker := time.NewTicker(a.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.stopCh:
				return
			case <-ticker.C:
				a.poll(ctx, out)
			}
		}
	}()
	return nil
}

func (a *GitHubCIAdapter) Stop() error {
	a.stopOnce.Do(func() { close(a.stopCh) })
	return nil
}

func (a *GitHubCIAdapter) poll(ctx context.Context, out chan<- adapter.Signal) {
	runs, err := a.fetchRuns(ctx)
	if err != nil {
		return
	}
	for _, run := range runs {
		if !a.isWatched(run.Conclusion) {
			continue
		}
		a.seenMu.Lock()
		already := a.seen[run.ID]
		if !already {
			a.seen[run.ID] = true
		}
		a.seenMu.Unlock()
		if already {
			continue
		}
		select {
		case <-a.stopCh:
			return
		case out <- adapter.Signal{
			Type:   adapter.SignalCIFailure,
			Source: "github-ci",
			Payload: map[string]any{
				"run_id":        run.ID,
				"workflow_name": run.Name,
				"conclusion":    run.Conclusion,
				"html_url":      run.HTMLURL,
				"head_sha":      run.HeadSHA,
				"repo":          a.repo,
				"is_flake":      false, // flake detection is Phase 5
			},
		}:
		}
	}
}

type workflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	HeadSHA    string `json:"head_sha"`
	WorkflowID int64  `json:"workflow_id"`
}

func (a *GitHubCIAdapter) fetchRuns(ctx context.Context) ([]workflowRun, error) {
	url := fmt.Sprintf("%s/repos/%s/actions/runs?status=completed&per_page=10", a.baseURL, a.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api status %d", resp.StatusCode)
	}

	var body struct {
		WorkflowRuns []workflowRun `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.WorkflowRuns, nil
}

func (a *GitHubCIAdapter) isWatched(conclusion string) bool {
	for _, w := range a.watch {
		if w == conclusion {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/adapter/githubci/... -v
```

Expected: all 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/githubci/
git commit -m "feat: GitHub CI adapter — polls Actions API for failed runs"
```

---

### Task 6: PR Creator

**Files:**
- Create: `internal/output/pr.go`
- Create: `internal/output/pr_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/output/pr_test.go`:

```go
package output_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initRepoWithRemote(t *testing.T) (localPath, remotePath string) {
	t.Helper()
	// Create a bare "remote" repo
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")

	// Clone it as the local working repo
	local := t.TempDir()
	runGit(t, local, "clone", remote, ".")
	runGit(t, local, "config", "user.email", "test@test.com")
	runGit(t, local, "config", "user.name", "Test")
	runGit(t, local, "commit", "--allow-empty", "-m", "initial")
	runGit(t, local, "push", "-u", "origin", "master")
	return local, remote
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestPRCreator_FallbackAPI(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/org/repo/pulls", r.URL.Path)
		require.Equal(t, "Bearer testtoken", r.Header.Get("Authorization"))
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://github.com/org/repo/pull/42",
		})
	}))
	defer server.Close()

	local, _ := initRepoWithRemote(t)

	// Write a file so there's something to commit and push
	require.NoError(t, os.WriteFile(filepath.Join(local, "fix.go"), []byte("package main"), 0644))
	out := output.New(local)
	branch, err := out.CommitBranch("task-pr-test", "sidecar: test fix")
	require.NoError(t, err)
	require.NotEmpty(t, branch)

	pc := output.NewPRCreatorWithBaseURL(local, "org/repo", "testtoken", server.URL)
	url, err := pc.Create(branch, "sidecar: test fix", "## Sidecar automated fix\n\nTest PR body.")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/org/repo/pull/42", url)
	assert.Equal(t, "sidecar: test fix", receivedBody["title"])
}

func TestPRCreator_DefaultBranch_FallsBackToMain(t *testing.T) {
	local, _ := initRepoWithRemote(t)
	pc := output.NewPRCreatorWithBaseURL(local, "org/repo", "token", "http://localhost:1")
	branch := pc.DefaultBranch()
	// Should return "master" (what we pushed) or "main" as fallback
	assert.NotEmpty(t, branch)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/output/... -v -run TestPRCreator
```

Expected: FAIL — `NewPRCreatorWithBaseURL`, `PRCreator`, `DefaultBranch` undefined.

- [ ] **Step 3: Implement pr.go**

Create `internal/output/pr.go`:

```go
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const githubAPIBase = "https://api.github.com"

// PRCreator creates GitHub pull requests by trying gh CLI first,
// then falling back to the GitHub REST API.
type PRCreator struct {
	repoPath string // local git repository path
	repoSlug string // "owner/repo"
	token    string // GitHub personal access token
	baseURL  string // GitHub API base URL (overridable for tests)
	client   *http.Client
}

// NewPRCreator creates a PRCreator using the real GitHub API.
func NewPRCreator(repoPath, repoSlug, token string) *PRCreator {
	return NewPRCreatorWithBaseURL(repoPath, repoSlug, token, githubAPIBase)
}

// NewPRCreatorWithBaseURL creates a PRCreator with a custom API base URL (used in tests).
func NewPRCreatorWithBaseURL(repoPath, repoSlug, token, baseURL string) *PRCreator {
	return &PRCreator{
		repoPath: repoPath,
		repoSlug: repoSlug,
		token:    token,
		baseURL:  baseURL,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Create pushes branch to origin and opens a PR. Returns the PR HTML URL.
func (p *PRCreator) Create(branch, title, body string) (string, error) {
	if err := p.pushBranch(branch); err != nil {
		return "", fmt.Errorf("pushing branch: %w", err)
	}

	base := p.DefaultBranch()

	// Try gh CLI first
	if url, err := p.createViaGH(branch, base, title, body); err == nil {
		return url, nil
	}

	// Fallback: GitHub REST API
	return p.createViaAPI(branch, base, title, body)
}

// DefaultBranch returns the default branch of the remote (e.g. "main" or "master").
func (p *PRCreator) DefaultBranch() string {
	out, err := exec.Command("git", "-C", p.repoPath,
		"symbolic-ref", "refs/remotes/origin/HEAD", "--short").Output()
	if err != nil {
		return "main"
	}
	branch := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(branch, "/"); idx >= 0 {
		branch = branch[idx+1:]
	}
	if branch == "" {
		return "main"
	}
	return branch
}

func (p *PRCreator) pushBranch(branch string) error {
	// Embed token in remote URL so push is authenticated without credential helper.
	remoteURL := fmt.Sprintf("https://%s@github.com/%s.git", p.token, p.repoSlug)
	if p.baseURL != githubAPIBase {
		// Test mode: push to origin directly (local bare repo)
		remoteURL = "origin"
	}
	out, err := exec.Command("git", "-C", p.repoPath, "push", remoteURL, branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push: %w\n%s", err, out)
	}
	return nil
}

func (p *PRCreator) createViaGH(branch, base, title, body string) (string, error) {
	out, err := exec.Command("gh", "pr", "create",
		"--repo", p.repoSlug,
		"--head", branch,
		"--base", base,
		"--title", title,
		"--body", body,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (p *PRCreator) createViaAPI(branch, base, title, body string) (string, error) {
	payload := map[string]string{
		"title": title,
		"body":  body,
		"head":  branch,
		"base":  base,
	}
	data, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/repos/%s/pulls", p.baseURL, p.repoSlug)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding github response: %w", err)
	}
	return result.HTMLURL, nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/output/... -v
```

Expected: all 5 tests pass (3 existing + 2 new).

- [ ] **Step 5: Commit**

```bash
git add internal/output/pr.go internal/output/pr_test.go
git commit -m "feat: PRCreator — gh CLI with GitHub API fallback"
```

---

### Task 7: Loop Updates — Triage + Autonomy Enforcement

**Files:**
- Modify: `internal/loop/loop.go`
- Modify: `internal/loop/loop_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/loop/loop_test.go`:

```go
func TestBuildSystemPrompt_CIFailure(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalCIFailure,
		Source: "github-ci",
		Payload: map[string]any{
			"workflow_name": "CI",
			"conclusion":    "failure",
			"head_sha":      "abc123",
			"html_url":      "https://github.com/org/repo/actions/runs/1",
		},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "CI")
	assert.Contains(t, prompt, "abc123")
	assert.Contains(t, prompt, "engineering agent")
}

func TestStatusConstants(t *testing.T) {
	assert.Equal(t, "skipped", loop.StatusSkipped)
	assert.Equal(t, "suggested", loop.StatusSuggested)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/loop/... -v
```

Expected: FAIL — `StatusSkipped` and `StatusSuggested` undefined; `BuildSystemPrompt` has no CI failure case.

- [ ] **Step 3: Update loop.go**

Replace `internal/loop/loop.go` with the Phase 2 version. Read the file first, then apply these changes:

**3a. Add new status constants** (extend the existing const block):

```go
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"   // triage decided no action needed
	StatusSuggested = "suggested" // suggest-only output, no code committed
)
```

**3b. Remove the `Triage TODO` comment** from the `Models` struct (it's now implemented):

```go
type Models struct {
	Coding string
	Triage string
}
```

**3c. Add imports** needed for triage and PR creation:

```go
import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/providers/anthropic"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tools/bash"
	"github.com/sausheong/harness/tools/file"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/output"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/sausheong/sidecar/internal/triage"
)
```

**3d. Replace `Loop.Run`** with the Phase 2 version:

```go
func (l *Loop) Run(ctx context.Context, sig adapter.Signal) error {
	task := &store.Task{
		WorkspaceID: l.workspace.ID,
		SignalType:  string(sig.Type),
		Summary:     summarize(sig),
	}
	if err := l.db.CreateTask(ctx, task); err != nil {
		return fmt.Errorf("creating task: %w", err)
	}

	// ── Triage ──────────────────────────────────────────────────────────────
	models := ResolveModels(l.cfg)
	tr, err := triage.Triage(ctx, l.provider, models.Triage, sig, l.cfg)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return fmt.Errorf("triage: %w", err)
	}
	_ = l.db.AppendTaskEvent(ctx, task.ID, "triage", map[string]any{
		"should_act":     tr.ShouldAct,
		"change_type":    tr.ChangeType,
		"autonomy_level": tr.AutonomyLevel,
		"reason":         tr.Reason,
	})
	if !tr.ShouldAct {
		slog.Info("sidecar skipping signal", "reason", tr.Reason, "task", task.ID)
		return l.db.UpdateTaskStatus(ctx, task.ID, StatusSkipped)
	}

	// ── Coding agent ─────────────────────────────────────────────────────────
	if err := l.db.UpdateTaskStatus(ctx, task.ID, StatusRunning); err != nil {
		return err
	}

	reg := tool.NewRegistry()
	reg.Register(&file.ReadFileTool{WorkDir: l.repoPath})
	if tr.AutonomyLevel != "suggest-only" {
		// Write tools only available when we are allowed to commit changes.
		reg.Register(&file.WriteFileTool{WorkDir: l.repoPath})
		reg.Register(&file.EditFileTool{WorkDir: l.repoPath})
		reg.Register(&bash.BashTool{WorkDir: l.repoPath})
	}

	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: l.provider,
			Tools:    reg,
			Session:  session.NewSession(task.ID.String(), "main"),
		},
		runtime.AgentSpec{
			ID:           task.ID.String(),
			Name:         "Sidecar",
			Model:        models.Coding,
			Workspace:    l.repoPath,
			SystemPrompt: BuildSystemPrompt(sig),
			MaxTurns:     20,
		},
	)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return fmt.Errorf("building runtime: %w", err)
	}
	defer rt.Close()

	events, err := rt.Run(ctx, userMessage(sig), nil)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return err
	}

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
	if agentErr != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return fmt.Errorf("agent error: %w", agentErr)
	}

	// ── Output routing ───────────────────────────────────────────────────────
	switch tr.AutonomyLevel {
	case "suggest-only":
		summary := textBuf.String()
		_ = l.db.AppendTaskEvent(ctx, task.ID, "suggestion", map[string]any{"summary": summary})
		slog.Info("sidecar suggestion recorded", "task", task.ID, "change_type", tr.ChangeType)
		return l.db.UpdateTaskStatus(ctx, task.ID, StatusSuggested)

	case "pull-request":
		out := output.New(l.repoPath)
		branch, err := out.CommitBranch(task.ID.String(), "sidecar: "+task.Summary)
		if err != nil {
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
			return err
		}
		if branch == output.BranchNoChanges {
			slog.Info("sidecar: no changes to commit", "task", task.ID)
			return l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)
		}
		repo, token := l.resolveRepoAndToken(sig)
		if repo != "" {
			pc := output.NewPRCreator(l.repoPath, repo, token)
			prURL, prErr := pc.Create(branch, "sidecar: "+task.Summary, l.prBody(sig, tr, task.ID.String()))
			if prErr != nil {
				slog.Warn("sidecar PR creation failed", "err", prErr, "branch", branch)
			} else {
				_ = l.db.AppendTaskEvent(ctx, task.ID, "pr_created", map[string]any{"url": prURL})
				slog.Info("sidecar PR created", "url", prURL, "task", task.ID)
			}
		}
		return l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)

	default: // "auto-commit"
		out := output.New(l.repoPath)
		branch, err := out.CommitBranch(task.ID.String(), "sidecar: "+task.Summary)
		if err != nil {
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
			return err
		}
		if branch != output.BranchNoChanges {
			slog.Info("sidecar committed changes", "branch", branch, "task", task.ID)
		}
		return l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)
	}
}

// resolveRepoAndToken looks up the repo slug and resolved token for a signal.
func (l *Loop) resolveRepoAndToken(sig adapter.Signal) (repo, token string) {
	// For CI failure signals, repo is in the payload.
	if r, ok := sig.Payload["repo"].(string); ok && r != "" {
		repo = r
	}
	// Find matching signal config to get token.
	for _, sc := range l.cfg.Signals {
		if sc.Repo == repo {
			t := sc.Token
			if strings.HasPrefix(t, "$") {
				t = os.Getenv(t[1:])
			}
			token = t
			return
		}
	}
	// Fallback to environment variable.
	token = os.Getenv("GITHUB_TOKEN")
	return
}

// prBody generates the PR description.
func (l *Loop) prBody(sig adapter.Signal, tr triage.TriageResult, taskID string) string {
	signalType := string(sig.Type)
	summary := summarize(sig)
	return fmt.Sprintf("## Sidecar automated fix\n\n**Signal:** %s — %s\n**Change type:** %s\n**Task ID:** %s\n\nThis PR was created automatically by Sidecar. Review and merge if the fix looks correct.",
		signalType, summary, tr.ChangeType, taskID)
}
```

**3e. Add `SignalCIFailure` case to `BuildSystemPrompt`:**

```go
case adapter.SignalCIFailure:
	workflow, _ := sig.Payload["workflow_name"].(string)
	sha, _ := sig.Payload["head_sha"].(string)
	url, _ := sig.Payload["html_url"].(string)
	return fmt.Sprintf(`%s

A GitHub Actions CI run failed:
Workflow: %s
Commit: %s
Run URL: %s

Investigate why the CI failed. Check recent changes, read failing test output if accessible,
and fix the root cause. Run tests locally to verify your fix before committing.`, base, workflow, sha, url)
```

**3f. Add `SignalCIFailure` case to `userMessage`:**

```go
case adapter.SignalCIFailure:
	workflow, _ := sig.Payload["workflow_name"].(string)
	sha, _ := sig.Payload["head_sha"].(string)
	return fmt.Sprintf("CI failure in workflow %q on commit %s. Investigate and fix.", workflow, sha)
```

**3g. Add `SignalCIFailure` case to `summarize`:**

```go
case adapter.SignalCIFailure:
	workflow, _ := sig.Payload["workflow_name"].(string)
	sha, _ := sig.Payload["head_sha"].(string)
	if len(sha) > 8 {
		sha = sha[:8]
	}
	return fmt.Sprintf("fix CI failure in %s @ %s", workflow, sha)
```

- [ ] **Step 4: Run all loop tests**

```bash
go test ./internal/loop/... -v
```

Expected: all 6 tests pass.

- [ ] **Step 5: Run full build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/loop/loop.go internal/loop/loop_test.go
git commit -m "feat: loop triage step, autonomy routing, suggest-only and PR output"
```

---

### Task 8: Wire `github-ci` in `buildAdapters`

**Files:**
- Modify: `internal/cli/attach.go`
- Modify: `internal/cli/attach_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/attach_test.go`:

```go
func TestAttachCmd_ValidatesGitHubToken_WhenCIAdapterConfigured(t *testing.T) {
	// Create a temp dir with a sidecar.yaml that has a github-ci adapter
	dir := t.TempDir()
	yaml := `
workspace:
  name: test
signals:
  - adapter: github-ci
    repo: org/repo
    token: $GITHUB_TOKEN
    watch: [failure]
autonomy:
  test_fixes: auto-commit
`
	err := os.WriteFile(filepath.Join(dir, "sidecar.yaml"), []byte(yaml), 0644)
	require.NoError(t, err)

	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"attach", dir})
	err = root.Execute()
	// Should fail because SIDECAR_DB_URL is unset (not because of github-ci)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}
```

Also add `"os"` and `"path/filepath"` to the import in `attach_test.go` if not already present.

- [ ] **Step 2: Run to confirm the test passes already**

```bash
go test ./internal/cli/... -v -run TestAttachCmd_ValidatesGitHubToken
```

Expected: PASS (the test verifies early DB URL validation, not the adapter itself — this is a smoke test).

- [ ] **Step 3: Add github-ci to buildAdapters**

Read `internal/cli/attach.go`, then update `buildAdapters` and add the new import:

```go
import (
	// existing imports...
	"os"
	"time"

	"github.com/sausheong/sidecar/internal/adapter/githubci"
)

func buildAdapters(repoPath string, cfg *config.Config) []adapter.Adapter {
	var adapters []adapter.Adapter
	for _, sig := range cfg.Signals {
		switch sig.Adapter {
		case "git":
			adapters = append(adapters, gitadapter.New(repoPath))
		case "schedule":
			if a, err := schedule.New(sig.Cron); err == nil {
				adapters = append(adapters, a)
			} else {
				log.Printf("invalid cron %q: %v", sig.Cron, err)
			}
		case "github-ci":
			token := sig.Token
			if len(token) > 0 && token[0] == '$' {
				token = os.Getenv(token[1:])
			}
			interval, err := time.ParseDuration(sig.PollInterval)
			if err != nil || interval <= 0 {
				interval = 60 * time.Second
			}
			adapters = append(adapters, githubci.New(sig.Repo, token, interval, sig.Watch))
		default:
			log.Printf("unknown adapter type %q, skipping", sig.Adapter)
		}
	}
	return adapters
}
```

- [ ] **Step 4: Run all CLI tests**

```bash
go test ./internal/cli/... -v
```

Expected: all tests pass.

- [ ] **Step 5: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass (store tests skip without `SIDECAR_TEST_DB_URL`).

- [ ] **Step 6: Final build**

```bash
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar --help
```

Expected: binary builds, help text lists `attach`, `task`, `status`.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/attach.go internal/cli/attach_test.go
git commit -m "feat: wire github-ci adapter in buildAdapters"
```

---

## Verification

After all tasks complete, verify the Phase 2 flow end-to-end with a mocked CI signal:

```bash
export SIDECAR_DB_URL="postgres://localhost/sidecar"
export ANTHROPIC_API_KEY="..."
export GITHUB_TOKEN="..."

go build -o /tmp/sidecar ./cmd/sidecar

# Attach to a test repo with github-ci configured in sidecar.yaml
cd /tmp/test-repo
/tmp/sidecar attach .

# In another terminal — submit a synthetic CI failure signal
/tmp/sidecar task "CI failed in workflow CI at commit abc123 — fix the failing tests"

# Check that triage ran and the task was classified
/tmp/sidecar status
```

Expected output from `status`:
```
STATUS     SIGNAL          SUMMARY                          CREATED
running    ondemand.task   fix CI failure in CI @ abc123    2026-05-08 ...
```
(or `completed`, `skipped`, `suggested` depending on triage result)
