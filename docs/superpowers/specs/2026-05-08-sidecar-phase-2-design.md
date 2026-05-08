# Sidecar Phase 2 — Reactive Runtime Design Spec

**Date:** 2026-05-08
**Status:** Approved
**Builds on:** Phase 1 (core runtime — complete)

---

## 1. Overview

Phase 2 makes Sidecar reactive to CI failures. It adds four capabilities to the Phase 1 system:

1. **GitHub CI adapter** — polls GitHub Actions for failed workflow runs
2. **Triage step** — cheap Haiku agent classifies signals before committing Sonnet
3. **Autonomy enforcement** — routes output through auto-commit / pull-request / suggest-only based on triage classification
4. **PR creation** — opens pull requests for changes requiring human review

**Key architectural decision:** Triage lives inside `Loop.Run` (not in the daemon). The daemon remains a pure signal router. The loop owns all agent orchestration.

---

## 2. New Components

```
internal/
  adapter/githubci/
    githubci.go          GitHub CI adapter (polls Actions API)
    githubci_test.go
  output/
    pr.go                PRCreator — gh CLI with GITHUB_TOKEN API fallback
    pr_test.go
  triage/
    triage.go            TriageResult type + triage prompt builder
    triage_test.go
```

**Modified files:**
- `internal/config/config.go` — add `Repo`, `Token`, `PollInterval` to `SignalConfig`
- `internal/adapter/adapter.go` — add `SignalCIFailure` constant
- `internal/loop/loop.go` — insert triage step, wire autonomy enforcement
- `internal/store/schema.sql` — add `skipped` and `suggested` as valid task statuses (TEXT, no constraint change needed)

---

## 3. sidecar.yaml Config Changes

New fields on `SignalConfig`:

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

**Token resolution:** If `Token` starts with `$`, it is resolved as `os.Getenv(token[1:])` at adapter startup. This avoids storing secrets in the config file.

Example `sidecar.yaml` signal entry:

```yaml
signals:
  - adapter: github-ci
    repo: myorg/payment-service
    token: $GITHUB_TOKEN
    poll_interval: 60s
    watch: [failure]
```

---

## 4. GitHub CI Adapter

**File:** `internal/adapter/githubci/githubci.go`

**Signal emitted:** `adapter.SignalCIFailure = "ci.failure"`

**Payload:**
```go
map[string]any{
    "run_id":        int64,   // GitHub Actions workflow run ID
    "workflow_name": string,  // e.g. "CI"
    "conclusion":    string,  // "failure", "timed_out", etc.
    "html_url":      string,  // link to the failed run
    "head_sha":      string,  // commit SHA that triggered the run
    "repo":          string,  // owner/repo
}
```

**Polling logic:**
1. On each tick, call `GET /repos/{owner}/{repo}/actions/runs?status=completed&per_page=10`
2. Filter runs where `conclusion` is in `watch` list (e.g. `["failure"]`)
3. Skip run IDs already seen (tracked in an in-memory `map[int64]bool`)
4. Emit one `SignalCIFailure` per new matching run
5. Default poll interval: 60 seconds

**Authentication:** `Authorization: Bearer {token}` header on all API calls.

**Flake detection:** A run is considered a flake if the same workflow (`workflow_name`) has failed more than once in the last 10 runs. Included in the signal payload as `"is_flake": bool` when `watch` includes `"flake"`.

---

## 5. Triage Step

**Location:** Inside `Loop.Run`, before the coding agent.

**Model:** `models.Triage` (default: `anthropic/claude-haiku-4-5-20251001`) — no Harness runtime, direct structured JSON call.

### TriageResult

```go
// internal/triage/triage.go
type TriageResult struct {
    ShouldAct     bool   // false = skip, no coding agent fired
    ChangeType    string // "test_fix" | "bug_fix" | "dependency_update" | "refactor" | "unknown"
    AutonomyLevel string // "auto-commit" | "pull-request" | "suggest-only"
    Reason        string // one sentence, stored in task_events
}
```

### Autonomy Resolution

1. Triage classifies `ChangeType` (e.g. `"test_fix"`)
2. Map to config field:

| ChangeType | Config field |
|-----------|-------------|
| `test_fix` | `cfg.Autonomy.TestFixes` |
| `bug_fix` | `cfg.Autonomy.BugFixes` |
| `dependency_update` | `cfg.Autonomy.DependencyUpdates` |
| `refactor` | `cfg.Autonomy.Refactoring` |
| `unknown` | `suggest-only` (safe default) |

3. If the mapped config field is empty, fall back to `suggest-only`.

### Fallback Behaviour

If the triage LLM call fails (parse error, API error, timeout):
- Log the error
- Return `TriageResult{ShouldAct: true, ChangeType: "unknown", AutonomyLevel: "suggest-only", Reason: "triage failed, defaulting to suggest-only"}`
- The loop continues conservatively rather than dropping the signal.

### Triage Prompt Structure

The prompt provides:
- Signal type and payload summary (CI failure log URL, git commit hash, on-demand description)
- The list of valid `ChangeType` values
- Instruction to respond with JSON only

Expected response:
```json
{
  "should_act": true,
  "change_type": "test_fix",
  "reason": "CI failure shows 3 test assertions failing in auth_test.go"
}
```

`AutonomyLevel` is not asked from the LLM — it is derived from `ChangeType` + config after parsing.

### Task Event

After triage, store a `task_event`:
```
type:    "triage"
payload: {"should_act": bool, "change_type": string, "autonomy_level": string, "reason": string}
```

---

## 6. Autonomy Enforcement in Loop.Run

Updated `Loop.Run` flow:

```
① Create task record (status: "pending")
② Run triage → TriageResult
   → Store task_event{"triage", result}
   → If !ShouldAct: update status "skipped", return nil
③ Update status "running"
④ Run coding agent (Sonnet) with BuildSystemPrompt(sig)
   → On EventError: update status "failed", return error
⑤ Route output by TriageResult.AutonomyLevel:
   "auto-commit"  → CommitBranch → status "completed"
   "pull-request" → CommitBranch → git push → CreatePR → store task_event{"pr_created"} → status "completed"
   "suggest-only" → coding agent runs in read-only analysis mode (no writes committed) → capture agent's final text response → store task_event{"suggestion", agent_summary} → status "suggested"
```

**New task statuses:**
- `skipped` — triage decided no action needed
- `suggested` — suggest-only output; agent analysis stored in task_events, no code changed

---

## 7. PR Creation

**File:** `internal/output/pr.go`

```go
type PRCreator struct {
    repoPath string // local git repo for push
    repoSlug string // "owner/repo"
    token    string // GitHub token
}

// Create pushes branch and opens a PR. Returns the PR URL.
func (p *PRCreator) Create(branch, title, body string) (string, error)
```

**Execution:**
1. `git push origin <branch>` (uses existing git credentials or token via `GIT_ASKPASS`)
2. Determine default branch: `git symbolic-ref refs/remotes/origin/HEAD --short | sed 's|origin/||'`; fall back to `"main"` if command fails
3. Try `gh pr create --title <title> --body <body> --head <branch> --base <default-branch>`
4. On failure (gh not found, not authenticated): POST to `https://api.github.com/repos/{slug}/pulls` with `Authorization: Bearer {token}`
5. Return PR URL

**PR body template:**
```
## Sidecar automated fix

**Signal:** {signal_type} — {summary}
**Change type:** {change_type}
**Task ID:** {task_id}

This PR was created automatically by Sidecar. Review and merge if the fix looks correct.
```

**Push authentication:** If `token` is set, configure git credential via `GIT_ASKPASS` or pass token in the remote URL for the push step.

---

## 8. Updated buildAdapters()

`internal/cli/attach.go` `buildAdapters()` adds the `github-ci` case:

```go
case "github-ci":
    token := sig.Token
    if strings.HasPrefix(token, "$") {
        token = os.Getenv(token[1:])
    }
    interval, _ := time.ParseDuration(sig.PollInterval)
    if interval == 0 {
        interval = 60 * time.Second
    }
    adapters = append(adapters, githubci.New(sig.Repo, token, interval, sig.Watch))
```

---

## 9. Environment Variables (Phase 2 additions)

| Variable | Required | Description |
|----------|----------|-------------|
| `GITHUB_TOKEN` | for CI adapter + PR creation | GitHub personal access token with `repo` scope |

---

## 10. New Signal Type

```go
// internal/adapter/adapter.go
const (
    SignalGitCommit    SignalType = "git.commit"
    SignalScheduleTick SignalType = "schedule.tick"
    SignalOnDemand     SignalType = "ondemand.task"
    SignalCIFailure    SignalType = "ci.failure"   // NEW in Phase 2
)
```

---

## 11. Non-Goals for Phase 2

- Webhook receiver (deferred to Phase 2 opt-in or Phase 5)
- GitLab CI, CircleCI, or other CI providers (Phase 5)
- Re-running failed CI after fix is merged
- Comment on PR with Sidecar analysis
- Slack/email notifications of PR creation
