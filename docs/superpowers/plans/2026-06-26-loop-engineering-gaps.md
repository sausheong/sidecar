# Loop-Engineering Gaps Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close two loop-engineering gaps (adversarial evaluator, worktree-isolated handoff) and two code-health blockers (broken build, secrets/dead-config) in sidecar.

**Architecture:** A new `internal/evaluate` package runs a fresh skeptic runtime (read+bash tools, no parent history) over the generator's diff and returns a PASS/REJECT verdict, gating both `auto-commit` and `pull-request` paths. A new `internal/worktree` package isolates each code-shipping task in its own `git worktree`. `internal/loop` orchestrates: worktree → agent → evaluate → commit-or-downgrade. Build and secrets fixes are mechanical.

**Tech Stack:** Go 1.25, harness (`runtime`, `llm`, `tool`, `tools/file`, `tools/bash`), pgx, testify, git CLI.

## Global Constraints

- Module path: `github.com/sausheong/sidecar`; harness via local replace `../harness`.
- Gates per task: `go build ./...` and `go test ./<package>/...` clean.
- Test style: external `_test` packages, testify `require`/`assert`, git-shelling tests mirror `internal/output/output_test.go` (`initRepo` helper).
- Test only pure/extracted functions (mirror `internal/triage`: `ParseTriageResponse`/`BuildTriageMessage` are tested, `Triage` is not). Do NOT build a fake `llm.LLMProvider`.
- Evaluator default: `verification.enabled` defaults to **true**.
- Fail closed: evaluator error or unparseable output ⇒ treat as REJECT (do not ship).
- Evaluator gates **both** `auto-commit` and `pull-request`.
- Commit messages end with the `Co-Authored-By: Claude <noreply@anthropic.com>` trailer.

---

### Task 1: Fix broken build (go.sum)

**Files:**
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nothing.
- Produces: a compiling module. All later tasks depend on this.

- [ ] **Step 1: Confirm the break**

Run: `go build ./... 2>&1 | head -5`
Expected: `missing go.sum entry for module providing package google.golang.org/genai`

- [ ] **Step 2: Tidy modules**

Run: `go mod tidy`

- [ ] **Step 3: Verify build and vet are clean**

Run: `go build ./... && go vet ./... && echo OK`
Expected: `OK` (no missing-go.sum errors)

- [ ] **Step 4: Verify existing hermetic tests pass**

Run: `go test ./... 2>&1 | tail -20`
Expected: all packages `ok` (no `[setup failed]`).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "fix(build): go mod tidy to add google.golang.org/genai go.sum entry

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: Secrets hygiene + dead uptime_fix change-type

**Files:**
- Create: `.gitignore`
- Modify: `internal/triage/triage.go` (the `triageSystemPrompt` const, ~line 32)
- Test: `internal/triage/triage_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `triageSystemPrompt` containing `uptime_fix` in its `change_type` enum.

- [ ] **Step 1: Write the failing test**

Add to `internal/triage/triage_test.go`:

```go
func TestTriageSystemPrompt_EnumeratesAllChangeTypes(t *testing.T) {
	msg := triage.BuildTriageMessage(adapter.Signal{
		Type:    adapter.SignalUptimeFailure,
		Payload: map[string]any{"url": "https://x", "failure_type": "wrong_status"},
	})
	// BuildTriageMessage is the user turn; the enum lives in the system prompt,
	// exposed for testing via SystemPrompt().
	_ = msg
	sp := triage.SystemPrompt()
	for _, ct := range []string{"test_fix", "bug_fix", "dependency_update", "refactor", "log_fix", "metric_fix", "uptime_fix"} {
		assert.Contains(t, sp, ct, "change_type %q must be enumerated in the triage system prompt", ct)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/triage/ -run TestTriageSystemPrompt_EnumeratesAllChangeTypes -v`
Expected: FAIL — `triage.SystemPrompt` undefined, then (once added) missing `uptime_fix`.

- [ ] **Step 3: Add the accessor and fix the enum**

In `internal/triage/triage.go`, update the `change_type` line in `triageSystemPrompt` to include `uptime_fix`:

```go
Valid change_type values: "test_fix", "bug_fix", "dependency_update", "refactor", "log_fix", "metric_fix", "uptime_fix", "unknown"
```

Add this exported accessor (after the const block):

```go
// SystemPrompt returns the triage system prompt. Exposed for tests that
// assert on its content.
func SystemPrompt() string { return triageSystemPrompt }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/triage/ -v`
Expected: PASS.

- [ ] **Step 5: Write .gitignore**

Create `.gitignore`:

```
# Local environment
.env

# Built binary
/sidecar
```

- [ ] **Step 6: Verify .env and binary are now ignored**

Run: `git check-ignore .env sidecar`
Expected: both paths printed (meaning they are ignored).

- [ ] **Step 7: Commit**

```bash
git add .gitignore internal/triage/triage.go internal/triage/triage_test.go
git commit -m "fix(triage): enumerate uptime_fix change_type; add .gitignore for .env and binary

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: Worktree isolation helper

**Files:**
- Create: `internal/worktree/worktree.go`
- Test: `internal/worktree/worktree_test.go`

**Interfaces:**
- Consumes: nothing (git CLI).
- Produces:
  - `type Worktree struct { Path string; Branch string }`
  - `func Create(repoPath, taskID string) (*Worktree, func() error, error)` — runs `git worktree add <tmpdir> -b sidecar/<taskID>` off current HEAD; returns the worktree, a cleanup func (`git worktree remove --force`), and error.

- [ ] **Step 1: Write the failing test**

Create `internal/worktree/worktree_test.go`:

```go
package worktree_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}

func TestCreate_IsolatedDirAndBranch(t *testing.T) {
	repo := initRepo(t)

	wt, cleanup, err := worktree.Create(repo, "task-123")
	require.NoError(t, err)
	require.NotNil(t, wt)

	assert.Equal(t, "sidecar/task-123", wt.Branch)
	assert.NotEqual(t, repo, wt.Path)

	// The worktree dir exists and is a working tree.
	info, err := os.Stat(wt.Path)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// A file written in the worktree does not appear in the main repo.
	require.NoError(t, os.WriteFile(filepath.Join(wt.Path, "only-here.txt"), []byte("x"), 0644))
	_, err = os.Stat(filepath.Join(repo, "only-here.txt"))
	assert.True(t, os.IsNotExist(err))

	// Cleanup removes the worktree dir but the branch survives in the main repo.
	require.NoError(t, cleanup())
	_, err = os.Stat(wt.Path)
	assert.True(t, os.IsNotExist(err))

	out, err := exec.Command("git", "-C", repo, "branch", "--list", "sidecar/task-123").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "sidecar/task-123")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worktree/ -v`
Expected: FAIL — package/`Create` undefined.

- [ ] **Step 3: Implement**

Create `internal/worktree/worktree.go`:

```go
// Package worktree isolates each code-shipping task in its own git worktree,
// so concurrent agent runs never share a working directory (the paper's
// "Tangled Loop" anti-pattern).
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree is an isolated git working directory on a dedicated branch.
type Worktree struct {
	Path   string
	Branch string
}

// Create adds a git worktree at a fresh temp dir on branch sidecar/<taskID>,
// based on the current HEAD of repoPath. The returned cleanup func removes the
// worktree directory (the branch persists in the shared .git so PR creation
// still works). Callers should defer cleanup().
func Create(repoPath, taskID string) (*Worktree, func() error, error) {
	branch := "sidecar/" + taskID
	dir, err := os.MkdirTemp("", "sidecar-wt-"+taskID+"-")
	if err != nil {
		return nil, nil, fmt.Errorf("creating worktree tmpdir: %w", err)
	}
	// git worktree add refuses a pre-existing non-empty dir; remove the stub.
	if err := os.RemoveAll(dir); err != nil {
		return nil, nil, fmt.Errorf("clearing worktree tmpdir: %w", err)
	}

	out, err := exec.Command("git", "-C", repoPath, "worktree", "add", "-b", branch, dir).CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("git worktree add: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	wt := &Worktree{Path: dir, Branch: branch}
	cleanup := func() error {
		o, e := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", dir).CombinedOutput()
		if e != nil {
			// Best-effort dir removal so we don't leak temp dirs on git error.
			_ = os.RemoveAll(dir)
			return fmt.Errorf("git worktree remove: %w\n%s", e, strings.TrimSpace(string(o)))
		}
		return nil
	}
	return wt, cleanup, nil
}

// ensure filepath import is used if later extended; currently unused paths.
var _ = filepath.Join
```

Note: remove the `filepath` import + `var _` line if the linter complains; they are only there to keep the import block stable. Prefer deleting both and the import.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worktree/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/
git commit -m "feat(worktree): isolated git worktree per code-shipping task

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: CommitInPlace for worktree commits

**Files:**
- Modify: `internal/output/output.go`
- Test: `internal/output/output_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func (o *Output) CommitInPlace(message string) (bool, error)` — when the working tree (already on its own branch, e.g. inside a worktree) is dirty, `add -A` + `commit`; returns `(true, nil)` if it committed, `(false, nil)` if clean. Leaves `CommitBranch` untouched for the non-worktree CLI path.

- [ ] **Step 1: Write the failing test**

Add to `internal/output/output_test.go`:

```go
func TestCommitInPlace_CommitsOnCurrentBranch(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)

	// Clean tree → no commit.
	did, err := o.CommitInPlace("sidecar: noop")
	require.NoError(t, err)
	assert.False(t, did)

	// Dirty tree → commits on the current branch (no new branch created).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("patched"), 0644))
	did, err = o.CommitInPlace("sidecar: applied fix")
	require.NoError(t, err)
	assert.True(t, did)

	out, err := exec.Command("git", "-C", dir, "log", "--oneline", "-1").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "sidecar: applied fix")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/output/ -run TestCommitInPlace -v`
Expected: FAIL — `CommitInPlace` undefined.

- [ ] **Step 3: Implement**

Add to `internal/output/output.go`:

```go
// CommitInPlace stages and commits all changes on the CURRENT branch (used
// when the caller is already inside an isolated worktree on its own branch).
// Returns true if a commit was made, false if the working tree was clean.
func (o *Output) CommitInPlace(message string) (bool, error) {
	if clean, err := o.isClean(); err != nil {
		return false, err
	} else if clean {
		return false, nil
	}
	cmds := [][]string{
		{"git", "-C", o.repoPath, "add", "-A"},
		{"git", "-C", o.repoPath, "commit", "-m", message},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return false, fmt.Errorf("git %s: %w\n%s", args[3], err, out)
		}
	}
	return true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/output/ -v`
Expected: PASS (including pre-existing CommitBranch tests).

- [ ] **Step 5: Commit**

```bash
git add internal/output/
git commit -m "feat(output): add CommitInPlace for committing inside a worktree branch

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: Evaluator — pure functions (prompt + verdict parsing)

**Files:**
- Create: `internal/evaluate/evaluate.go`
- Test: `internal/evaluate/evaluate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Verdict struct { Pass bool; Reasons string }`
  - `func ParseVerdict(raw string) (Verdict, error)` — tolerant first-`{`/last-`}` extraction (mirrors `triage.ParseTriageResponse`); JSON shape `{"pass": bool, "reasons": string}`.
  - `func BuildEvalMessage(taskSummary, diff string) string` — user turn embedding summary + diff.
  - `const evaluatorSystemPrompt` + `func SystemPrompt() string` accessor.

- [ ] **Step 1: Write the failing tests**

Create `internal/evaluate/evaluate_test.go`:

```go
package evaluate_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/evaluate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVerdict_Pass(t *testing.T) {
	v, err := evaluate.ParseVerdict(`{"pass": true, "reasons": "tests pass"}`)
	require.NoError(t, err)
	assert.True(t, v.Pass)
}

func TestParseVerdict_Reject(t *testing.T) {
	v, err := evaluate.ParseVerdict(`{"pass": false, "reasons": "missing nil check"}`)
	require.NoError(t, err)
	assert.False(t, v.Pass)
	assert.Contains(t, v.Reasons, "nil check")
}

func TestParseVerdict_Fenced(t *testing.T) {
	v, err := evaluate.ParseVerdict("```json\n{\"pass\": true, \"reasons\": \"ok\"}\n```")
	require.NoError(t, err)
	assert.True(t, v.Pass)
}

func TestParseVerdict_Garbage(t *testing.T) {
	_, err := evaluate.ParseVerdict("not json at all")
	assert.Error(t, err)
}

func TestBuildEvalMessage_EmbedsDiffAndSummary(t *testing.T) {
	msg := evaluate.BuildEvalMessage("fix CI failure", "diff --git a/x b/x")
	assert.Contains(t, msg, "fix CI failure")
	assert.Contains(t, msg, "diff --git a/x b/x")
}

func TestSystemPrompt_IsAdversarial(t *testing.T) {
	sp := evaluate.SystemPrompt()
	assert.Contains(t, sp, "BROKEN")
	assert.Contains(t, sp, "pass")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/evaluate/ -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement pure parts**

Create `internal/evaluate/evaluate.go`:

```go
// Package evaluate implements the generator/evaluator split: a fresh skeptic
// runtime reviews the generator's diff and can REJECT it before any commit.
// This closes the "Nodding Loop" — an agent grading its own work.
package evaluate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Verdict is the evaluator's decision on a diff.
type Verdict struct {
	Pass    bool
	Reasons string
}

const evaluatorSystemPrompt = `You are an adversarial code reviewer for an autonomous engineering system.
A generator agent just produced a diff. ASSUME THIS DIFF IS BROKEN until proven otherwise.
Do NOT praise. Do NOT trust the author's intent. Your job is to find what fails.

Check, in order:
1. Does it build/compile? Run the build.
2. Do the tests pass? Run them — do not just read them.
3. Edge cases the author skipped.
4. Does the behaviour actually match the stated task?

You have read and shell access. Use them to verify by acting, not by reading.

Respond with ONLY valid JSON, no prose:
{"pass": false, "reasons": "one or two sentences citing the specific failure"}
Set pass=true ONLY if every check above holds.`

// SystemPrompt returns the evaluator system prompt. Exposed for tests.
func SystemPrompt() string { return evaluatorSystemPrompt }

// BuildEvalMessage builds the user-turn message: the task plus the diff to judge.
func BuildEvalMessage(taskSummary, diff string) string {
	return fmt.Sprintf(`Task the generator was asked to do: %s

Here is the generator's uncommitted diff. Verify it by building and running tests, then return your verdict.

--- DIFF ---
%s
--- END DIFF ---`, taskSummary, diff)
}

// ParseVerdict extracts the JSON verdict from the evaluator's response,
// tolerating code fences and surrounding prose.
func ParseVerdict(raw string) (Verdict, error) {
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "{"); start != -1 {
		if end := strings.LastIndex(raw, "}"); end != -1 && end >= start {
			raw = raw[start : end+1]
		}
	}
	var resp struct {
		Pass    bool   `json:"pass"`
		Reasons string `json:"reasons"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return Verdict{}, fmt.Errorf("parsing verdict JSON: %w", err)
	}
	return Verdict{Pass: resp.Pass, Reasons: resp.Reasons}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/evaluate/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/evaluate/
git commit -m "feat(evaluate): adversarial verdict parsing and prompt construction

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: Evaluator — runtime orchestration (Evaluate)

**Files:**
- Modify: `internal/evaluate/evaluate.go`
- Test: `internal/evaluate/evaluate_test.go` (no new live test; covered by build + later loop wiring)

**Interfaces:**
- Consumes: harness `runtime`, `llm`, `tool`, `tools/file`, `tools/bash`, `session`; `os/exec` for `git diff`.
- Produces: `func Evaluate(ctx context.Context, provider llm.LLMProvider, model, workDir, taskSummary string) (Verdict, error)`.

- [ ] **Step 1: Implement Evaluate**

Append to `internal/evaluate/evaluate.go` (add imports `context`, `os/exec`, and the harness packages):

```go
// Evaluate builds a fresh skeptic runtime over the uncommitted diff in workDir
// and returns its verdict. It does NOT inherit the generator's conversation —
// the evaluator must carry none of the generator's self-persuasion.
//
// On any setup/run error the caller should fail closed (treat as REJECT); this
// function returns the error so the caller can record it.
func Evaluate(ctx context.Context, provider llm.LLMProvider, model, workDir, taskSummary string) (Verdict, error) {
	diffOut, err := exec.Command("git", "-C", workDir, "diff", "HEAD").CombinedOutput()
	if err != nil {
		return Verdict{}, fmt.Errorf("git diff: %w\n%s", err, strings.TrimSpace(string(diffOut)))
	}
	diff := strings.TrimSpace(string(diffOut))
	if diff == "" {
		// No diff to judge — nothing was shipped; treat as a trivial pass.
		return Verdict{Pass: true, Reasons: "no changes to evaluate"}, nil
	}

	reg := tool.NewRegistry()
	reg.Register(&file.ReadFileTool{WorkDir: workDir})
	reg.Register(&bash.BashTool{WorkDir: workDir})

	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: provider,
			Tools:    reg,
			Session:  session.NewSession("evaluate-"+sessionSuffix(taskSummary), "main"),
		},
		runtime.AgentSpec{
			ID:           "evaluate",
			Name:         "Evaluator",
			Model:        model,
			Workspace:    workDir,
			SystemPrompt: evaluatorSystemPrompt,
			MaxTurns:     8,
		},
	)
	if err != nil {
		return Verdict{}, fmt.Errorf("building evaluator runtime: %w", err)
	}
	defer rt.Close()

	events, err := rt.Run(ctx, BuildEvalMessage(taskSummary, diff), nil)
	if err != nil {
		return Verdict{}, fmt.Errorf("running evaluator: %w", err)
	}

	var sb strings.Builder
	for ev := range events {
		if ev.Type == runtime.EventTextDelta {
			sb.WriteString(ev.Text)
		}
		if ev.Type == runtime.EventError && ev.Error != nil {
			return Verdict{}, fmt.Errorf("evaluator event error: %w", ev.Error)
		}
	}

	return ParseVerdict(sb.String())
}

// sessionSuffix derives a short stable-ish suffix for the session id.
func sessionSuffix(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ReplaceAll(s, " ", "-")
}
```

Add to the import block:

```go
	"context"
	"os/exec"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tools/bash"
	"github.com/sausheong/harness/tools/file"
```

- [ ] **Step 2: Verify build + existing tests**

Run: `go build ./... && go test ./internal/evaluate/ -v`
Expected: build OK; pure-function tests still PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/evaluate/
git commit -m "feat(evaluate): fresh skeptic runtime over the diff (Evaluate)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: Config — verification block + evaluator model

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `Config.Verification VerificationConfig` (yaml `verification`).
  - `type VerificationConfig struct { Enabled *bool }` (yaml `enabled`) — pointer so "absent" can default to true.
  - `func (c *Config) VerificationEnabled() bool` — returns true when unset or true.
  - `ModelConfig.Evaluator string` (yaml `evaluator`).

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go`:

```go
func TestVerificationEnabled_DefaultsTrueWhenAbsent(t *testing.T) {
	cfg := &config.Config{}
	assert.True(t, cfg.VerificationEnabled())
}

func TestVerificationEnabled_RespectsExplicitFalse(t *testing.T) {
	f := false
	cfg := &config.Config{Verification: config.VerificationConfig{Enabled: &f}}
	assert.False(t, cfg.VerificationEnabled())
}

func TestVerificationEnabled_RespectsExplicitTrue(t *testing.T) {
	tr := true
	cfg := &config.Config{Verification: config.VerificationConfig{Enabled: &tr}}
	assert.True(t, cfg.VerificationEnabled())
}
```

Confirm `config_test.go` imports `testify/assert`; add it if missing.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestVerificationEnabled -v`
Expected: FAIL — `Verification`/`VerificationConfig`/`VerificationEnabled` undefined.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, add to the `Config` struct:

```go
	Verification  VerificationConfig   `yaml:"verification"`
```

Add the type and accessor:

```go
// VerificationConfig controls the adversarial evaluator gate.
type VerificationConfig struct {
	// Enabled gates auto-commit and pull-request changes behind the
	// evaluator. Pointer so an absent value defaults to true.
	Enabled *bool `yaml:"enabled"`
}

// VerificationEnabled reports whether the evaluator gate is on. Defaults to
// true when unset — a default-off gate would not close the Nodding Loop.
func (c *Config) VerificationEnabled() bool {
	if c.Verification.Enabled == nil {
		return true
	}
	return *c.Verification.Enabled
}
```

Add to `ModelConfig`:

```go
	Evaluator string `yaml:"evaluator"`
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): verification.enabled (default true) and models.evaluator

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: Loop — resolve evaluator model + gate helper (pure)

**Files:**
- Modify: `internal/loop/loop.go`
- Test: `internal/loop/loop_test.go`

**Interfaces:**
- Consumes: `config.Config`, `evaluate.Verdict`.
- Produces:
  - `Models.Evaluator string`; `ResolveModels` sets `Evaluator` = `cfg.Models.Evaluator` or falls back to the resolved `Coding` model.
  - `func shipsCode(autonomyLevel string) bool` → true for `auto-commit` and `pull-request`.
  - `func gateAllowsCommit(verdict evaluate.Verdict, evalErr error) bool` → true only when `evalErr == nil && verdict.Pass` (fail closed).

- [ ] **Step 1: Write the failing tests**

Add to `internal/loop/loop_test.go` (add imports `github.com/sausheong/sidecar/internal/evaluate` and `errors`):

```go
func TestResolveModels_EvaluatorDefaultsToCoding(t *testing.T) {
	cfg := &config.Config{}
	m := loop.ResolveModels(cfg)
	assert.Equal(t, m.Coding, m.Evaluator)
}

func TestResolveModels_EvaluatorOverride(t *testing.T) {
	cfg := &config.Config{Models: config.ModelConfig{Evaluator: "anthropic/claude-opus-4-8"}}
	m := loop.ResolveModels(cfg)
	assert.Equal(t, "anthropic/claude-opus-4-8", m.Evaluator)
}

func TestShipsCode(t *testing.T) {
	assert.True(t, loop.ShipsCode("auto-commit"))
	assert.True(t, loop.ShipsCode("pull-request"))
	assert.False(t, loop.ShipsCode("suggest-only"))
	assert.False(t, loop.ShipsCode("notify"))
}

func TestGateAllowsCommit(t *testing.T) {
	assert.True(t, loop.GateAllowsCommit(evaluate.Verdict{Pass: true}, nil))
	assert.False(t, loop.GateAllowsCommit(evaluate.Verdict{Pass: false}, nil))
	// Fail closed: evaluator error blocks the commit even on a pass verdict.
	assert.False(t, loop.GateAllowsCommit(evaluate.Verdict{Pass: true}, errors.New("boom")))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/loop/ -run 'TestResolveModels_Eval|TestShipsCode|TestGateAllowsCommit' -v`
Expected: FAIL — symbols undefined.

- [ ] **Step 3: Implement**

In `internal/loop/loop.go`: add `Evaluator string` to the `Models` struct; in `ResolveModels`, after computing `m.Coding`, add:

```go
	m.Evaluator = m.Coding
	if cfg.Models.Evaluator != "" {
		m.Evaluator = cfg.Models.Evaluator
	}
```

Add the two pure helpers (exported for testing):

```go
// ShipsCode reports whether an autonomy level results in committed code and
// therefore must pass the evaluator gate.
func ShipsCode(autonomyLevel string) bool {
	return autonomyLevel == "auto-commit" || autonomyLevel == "pull-request"
}

// GateAllowsCommit reports whether the evaluator verdict permits committing.
// Fails closed: any evaluator error blocks the commit regardless of verdict.
func GateAllowsCommit(verdict evaluate.Verdict, evalErr error) bool {
	return evalErr == nil && verdict.Pass
}
```

Add `"github.com/sausheong/sidecar/internal/evaluate"` to the loop imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/loop/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/loop/
git commit -m "feat(loop): evaluator model resolution + ShipsCode/GateAllowsCommit gate helpers

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 9: Loop — wire worktree + evaluator into Run

**Files:**
- Modify: `internal/loop/loop.go`
- Test: build + full hermetic suite (Run itself is not unit-tested, per repo pattern)

**Interfaces:**
- Consumes: `worktree.Create`, `evaluate.Evaluate`, `output.CommitInPlace`, the helpers from Task 8.
- Produces: updated `Run` control flow for code-shipping levels.

- [ ] **Step 1: Implement the new control flow**

In `internal/loop/loop.go`, replace the section from the coding-agent setup through output routing with worktree-aware logic. Key changes:

1. Add imports: `github.com/sausheong/sidecar/internal/evaluate`, `github.com/sausheong/sidecar/internal/worktree`.

2. After triage decides to act and before building the coding registry, choose the working directory:

```go
	// Code-shipping autonomy levels run in an isolated worktree so concurrent
	// signals never share a working tree. suggest-only/notify run in-repo.
	workDir := l.repoPath
	var wtCleanup func() error
	var wt *worktree.Worktree
	if ShipsCode(tr.AutonomyLevel) {
		w, cleanup, wErr := worktree.Create(l.repoPath, task.ID.String())
		if wErr != nil {
			slog.Warn("worktree create failed; falling back to in-repo execution", "err", wErr, "task", task.ID)
			_ = l.db.AppendTaskEvent(ctx, task.ID, "worktree_degraded", map[string]any{"error": wErr.Error()})
		} else {
			wt, wtCleanup, workDir = w, cleanup, w.Path
			defer func() {
				if cErr := wtCleanup(); cErr != nil {
					slog.Warn("worktree cleanup failed", "err", cErr, "task", task.ID)
				}
			}()
		}
	}
```

3. Change the coding registry + AgentSpec to use `workDir` instead of `l.repoPath`:
   - `reg.Register(&file.ReadFileTool{WorkDir: workDir})` and likewise for Write/Edit/Bash.
   - `spec.Workspace = workDir`.

4. After the agent run completes successfully (`agentErr == nil`), and **before** output routing, run the evaluator gate for code-shipping levels:

```go
	// ── Adversarial evaluation gate ──────────────────────────────────────────
	if ShipsCode(tr.AutonomyLevel) && l.cfg.VerificationEnabled() {
		verdict, evalErr := evaluate.Evaluate(ctx, l.provider, models.Evaluator, workDir, task.Summary)
		if evalErr != nil {
			slog.Warn("evaluator error; failing closed (downgrade to suggestion)", "err", evalErr, "task", task.ID)
		}
		_ = l.db.AppendTaskEvent(ctx, task.ID, "evaluation", map[string]any{
			"pass":    verdict.Pass,
			"reasons": verdict.Reasons,
			"model":   models.Evaluator,
			"error":   errString(evalErr),
		})
		if !GateAllowsCommit(verdict, evalErr) {
			slog.Info("evaluator rejected change; recording as suggestion", "task", task.ID, "reasons", verdict.Reasons)
			_ = l.db.AppendTaskEvent(ctx, task.ID, "suggestion", map[string]any{
				"summary": textBuf.String(),
				"rejected_reasons": verdict.Reasons,
			})
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusSuggested)
			l.dispatcher.Fire(ctx, notify.EventSuggested, sig, task)
			return nil
		}
	}
```

5. In the output-routing switch, replace `output.New(l.repoPath)` + `CommitBranch` for the `pull-request` and `auto-commit` cases with worktree-aware commit. When a worktree was created, commit in place and use `wt.Branch`; otherwise keep the old `CommitBranch` behaviour. Concretely, add a helper closure before the switch:

```go
	commit := func() (string, error) {
		out := output.New(workDir)
		if wt != nil {
			did, err := out.CommitInPlace("sidecar: " + task.Summary)
			if err != nil {
				return "", err
			}
			if !did {
				return output.BranchNoChanges, nil
			}
			return wt.Branch, nil
		}
		return out.CommitBranch(task.ID.String(), "sidecar: "+task.Summary)
	}
```

Then in both the `pull-request` and `default` (auto-commit) cases, call `branch, err := commit()` instead of `out.CommitBranch(...)`. The rest of each case (PR creation, status updates, notifications) is unchanged. PR creation must use `l.repoPath` for the `PRCreator` (the shared repo holds the branch), not `workDir`.

6. Add the small helper:

```go
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 3: Run the full hermetic suite**

Run: `go test ./... 2>&1 | tail -25`
Expected: all packages `ok`.

- [ ] **Step 4: Vet**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/loop/
git commit -m "feat(loop): run code-shipping tasks in a worktree, gate commits on the evaluator

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 10: Docs — sidecar.yaml + CLAUDE.md

**Files:**
- Modify: `sidecar.yaml`, `CLAUDE.md`

**Interfaces:**
- Consumes: nothing.
- Produces: documentation reflecting the new config and behaviour.

- [ ] **Step 1: Update sidecar.yaml**

Add to `sidecar.yaml`:

```yaml
verification:
  enabled: true        # adversarial evaluator gates auto-commit & pull-request (default true)

models:
  # ... existing planning/coding/triage ...
  evaluator: anthropic/claude-sonnet-4-6   # optional; defaults to coding model
```

Also add the missing autonomy keys the code reads:

```yaml
autonomy:
  # ... existing ...
  log_fixes: suggest-only
  metric_fixes: suggest-only
  uptime_fixes: notify
```

- [ ] **Step 2: Update CLAUDE.md status table**

In `CLAUDE.md`, update the implementation-status table: mark Phase 2 (Reactive) and the verification gate as complete, and add a line under "Key Design Decisions" describing the evaluator gate and worktree isolation:

```markdown
- **Adversarial evaluator** — code-shipping changes (`auto-commit`/`pull-request`) are gated by a fresh skeptic runtime (`internal/evaluate`) that runs tests over the diff and can REJECT (downgrades to a suggestion). Default on via `verification.enabled`.
- **Worktree isolation** — each code-shipping task runs in its own `git worktree` (`internal/worktree`), so concurrent signals never share a working tree.
```

- [ ] **Step 3: Verify build still clean (docs-only, sanity)**

Run: `go build ./... && echo OK`
Expected: `OK`.

- [ ] **Step 4: Commit**

```bash
git add sidecar.yaml CLAUDE.md
git commit -m "docs: document evaluator gate, worktree isolation, and missing autonomy keys

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec coverage:** build fix (T1), secrets+uptime_fix (T2), worktree (T3/T4/T9), evaluator (T5/T6/T7/T8/T9), config default-on (T7), gate-both + fail-closed (T8/T9), docs (T10). All spec items mapped.
- **Type consistency:** `Verdict{Pass,Reasons}`, `Evaluate(ctx,provider,model,workDir,taskSummary)`, `worktree.Create→(*Worktree,func()error,error)`, `CommitInPlace(message)→(bool,error)`, `Models.Evaluator`, `VerificationEnabled()`, `ShipsCode`/`GateAllowsCommit` are used identically across tasks.
- **Fail-closed** is realised in `GateAllowsCommit` (T8) and consumed in T9.
- **Non-worktree CLI path** (`internal/cli`) is untouched: `CommitBranch` retained; only loop uses the worktree path.
