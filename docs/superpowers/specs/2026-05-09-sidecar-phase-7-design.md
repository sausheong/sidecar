# Sidecar Phase 7 — Additional CI Providers Design Spec

**Date:** 2026-05-09
**Status:** Approved
**Builds on:** Phase 1–6 (complete)

---

## 1. Overview

Phase 7 adds two new CI adapter packages — GitLab CI and CircleCI — following the `githubci` adapter pattern exactly. No new signal type, no new config types, no new autonomy levels. Both adapters reuse the existing `SignalCIFailure` signal and the existing `SignalConfig` fields: `repo`, `token`, `watch`, `poll_interval`.

One cross-cutting change: `BuildSystemPrompt` and `BuildTriageMessage` currently hardcode "GitHub Actions CI" — both are updated to use `sig.Source` (`"github-ci"`, `"gitlab-ci"`, or `"circleci"`) so all three CI adapters produce accurate prompts.

---

## 2. New Components

```
internal/
  adapter/
    gitlabci/
      gitlabci.go       CREATE — GitLabCIAdapter
      gitlabci_test.go  CREATE — httptest-based tests
    circleci/
      circleci.go       CREATE — CircleCIAdapter
      circleci_test.go  CREATE — httptest-based tests
  loop/
    loop.go             MODIFY — generalize SignalCIFailure prompt to use sig.Source
    loop_test.go        MODIFY — add source-specific prompt tests
  triage/
    triage.go           MODIFY — generalize SignalCIFailure triage message
    triage_test.go      MODIFY — add source-specific triage test
  cli/
    attach.go           MODIFY — wire gitlab-ci and circleci adapters
```

---

## 3. GitLabCIAdapter (`internal/adapter/gitlabci/gitlabci.go`)

### Struct

```go
type GitLabCIAdapter struct {
    repo         string // "namespace/project", e.g. "mygroup/myproject"
    token        string
    pollInterval time.Duration
    watch        []string // pipeline statuses to act on: "failed", "canceled", etc.
    baseURL      string

    seen     map[int64]bool
    seenMu   sync.Mutex
    stopOnce sync.Once
    stopCh   chan struct{}
    client   *http.Client
}
```

### Constructors

```go
func New(repo, token string, pollInterval time.Duration, watch []string) *GitLabCIAdapter
func NewWithBaseURL(repo, token string, pollInterval time.Duration, watch []string, baseURL string) *GitLabCIAdapter
```

`Name() string` returns `"gitlab-ci"`.

### API

`GET {baseURL}/api/v4/projects/{url.PathEscape(repo)}/pipelines?order_by=id&sort=desc&per_page=10`

- Auth: `PRIVATE-TOKEN: <token>` header
- `repo` is URL-encoded using `url.PathEscape` (converts `/` to `%2F`)
- Fetches 10 most recent pipelines; filters by `watch` list on pipeline `status` field
- Deduplicates by pipeline `id` (`int64`)

### Pipeline response shape

```json
[
  {
    "id": 1234,
    "status": "failed",
    "ref": "main",
    "sha": "abc123",
    "web_url": "https://gitlab.com/mygroup/myproject/-/pipelines/1234"
  }
]
```

### Signal payload

```go
map[string]any{
    "pipeline_id":   pipeline.ID,       // int64
    "workflow_name": pipeline.Ref,      // branch or tag name
    "conclusion":    pipeline.Status,   // "failed", "canceled", etc.
    "html_url":      pipeline.WebURL,
    "head_sha":      pipeline.SHA,
    "repo":          a.repo,
    "is_flake":      false,
}
```

### sidecar.yaml example

```yaml
signals:
  - adapter: gitlab-ci
    repo: "mygroup/myproject"
    token: $GITLAB_TOKEN
    poll_interval: "60s"
    watch:
      - "failed"
      - "canceled"
```

### Tests (`gitlabci_test.go`)

- `TestGitLabCIAdapter_DetectsFailure` — server returns a failed pipeline; assert signal emitted with correct payload fields
- `TestGitLabCIAdapter_NoDuplicates` — same failed pipeline returned twice; assert signal emitted once
- `TestGitLabCIAdapter_IgnoresNonWatchedStatus` — pipeline with `"success"` status; assert no signal

---

## 4. CircleCIAdapter (`internal/adapter/circleci/circleci.go`)

### Two-call design

CircleCI's v2 API separates pipeline metadata from workflow outcomes. Two calls are needed per unseen pipeline:

1. `GET {baseURL}/api/v2/project/{project-slug}/pipeline?limit=10` — recent pipelines
2. `GET {baseURL}/api/v2/pipeline/{id}/workflow` — workflow status per unseen pipeline

A pipeline is added to `seen` immediately on fetch (before checking workflows) to avoid re-fetching workflows on every poll.

### Struct

```go
type CircleCIAdapter struct {
    repo         string // project slug, e.g. "gh/myorg/myrepo"
    token        string
    pollInterval time.Duration
    watch        []string // workflow statuses: "failed", "error", etc.
    baseURL      string

    seen     map[string]bool // keyed by pipeline UUID string
    seenMu   sync.Mutex
    stopOnce sync.Once
    stopCh   chan struct{}
    client   *http.Client
}
```

### Constructors

```go
func New(repo, token string, pollInterval time.Duration, watch []string) *CircleCIAdapter
func NewWithBaseURL(repo, token string, pollInterval time.Duration, watch []string, baseURL string) *CircleCIAdapter
```

`Name() string` returns `"circleci"`.

### API

**Step 1:** `GET {baseURL}/api/v2/project/{repo}/pipeline?limit=10`

- Auth: `Circle-Token: <token>` header

**Step 2:** For each pipeline ID not in `seen`: `GET {baseURL}/api/v2/pipeline/{id}/workflow`

- Returns list of workflows; if any workflow `status` matches the `watch` list, emit signal using the **first matching workflow's** name and status in the payload

### Pipeline response shape (step 1)

```json
{
  "items": [
    {
      "id": "uuid-string",
      "number": 42,
      "vcs": { "revision": "abc123" }
    }
  ]
}
```

### Workflow response shape (step 2)

```json
{
  "items": [
    { "name": "build-and-test", "status": "failed" }
  ]
}
```

### Signal payload

```go
map[string]any{
    "pipeline_id":   pipeline.Number,  // int64, human-readable pipeline number
    "workflow_name": workflow.Name,
    "conclusion":    workflow.Status,
    "html_url":      fmt.Sprintf("https://app.circleci.com/pipelines/%s/%d", a.repo, pipeline.Number),
    "head_sha":      pipeline.VCS.Revision,
    "repo":          a.repo,
    "is_flake":      false,
}
```

### sidecar.yaml example

```yaml
signals:
  - adapter: circleci
    repo: "gh/myorg/myrepo"
    token: $CIRCLECI_TOKEN
    poll_interval: "60s"
    watch:
      - "failed"
      - "error"
```

### Tests (`circleci_test.go`)

- `TestCircleCIAdapter_DetectsFailure` — pipeline server returns one pipeline; workflow server returns failed workflow; assert signal emitted
- `TestCircleCIAdapter_NoDuplicates` — same pipeline returned on two polls; assert signal emitted once
- `TestCircleCIAdapter_IgnoresNonWatchedStatus` — workflow with `"success"` status; assert no signal

---

## 5. Loop Generalization (`internal/loop/loop.go`)

Replace the hardcoded "GitHub Actions" reference in `BuildSystemPrompt`:

**Before:**
```
A GitHub Actions CI run failed:
Workflow: %s
...
```

**After:**
```go
case adapter.SignalCIFailure:
    workflow, _ := sig.Payload["workflow_name"].(string)
    sha, _ := sig.Payload["head_sha"].(string)
    url, _ := sig.Payload["html_url"].(string)
    return fmt.Sprintf(`%s

A CI run failed in %s:
Workflow: %s
Commit: %s
Run URL: %s

Investigate why the CI failed. Check recent changes, read failing test output if accessible,
and fix the root cause. Run tests locally to verify your fix before committing.`, base, sig.Source, workflow, sha, url)
```

`userMessage` and `summarize` cases for `SignalCIFailure` are already generic — no changes needed.

New test: `TestBuildSystemPrompt_CIFailure_GitLab` verifies prompt contains `"gitlab-ci"` when `sig.Source == "gitlab-ci"`.

---

## 6. Triage Generalization (`internal/triage/triage.go`)

Replace the hardcoded "GitHub Actions CI failure:" in `BuildTriageMessage`:

**Before:**
```
GitHub Actions CI failure:
Workflow: %s
...
```

**After:**
```go
case adapter.SignalCIFailure:
    workflow, _ := sig.Payload["workflow_name"].(string)
    conclusion, _ := sig.Payload["conclusion"].(string)
    sha, _ := sig.Payload["head_sha"].(string)
    url, _ := sig.Payload["html_url"].(string)
    repo, _ := sig.Payload["repo"].(string)
    isFlake, _ := sig.Payload["is_flake"].(bool)
    return fmt.Sprintf("CI failure in %s:\nWorkflow: %s\nConclusion: %s\nCommit: %s\nURL: %s\nRepo: %s\nFlaky: %v\n\nShould this be fixed automatically?",
        sig.Source, workflow, conclusion, sha, url, repo, isFlake)
```

New test: `TestBuildTriageMessage_CIFailure_CircleCI` verifies triage message contains `"circleci"`.

---

## 7. CLI Wiring (`internal/cli/attach.go`)

Add to `buildAdapters` switch:

```go
case "gitlab-ci":
    token := sig.ResolveToken()
    interval := sig.ParsedPollInterval()
    adapters = append(adapters, gitlabci.New(sig.Repo, token, interval, sig.Watch))

case "circleci":
    token := sig.ResolveToken()
    interval := sig.ParsedPollInterval()
    adapters = append(adapters, circleci.New(sig.Repo, token, interval, sig.Watch))
```

---

## 8. Non-Goals for Phase 7

- Jenkins, Bitbucket Pipelines, Azure DevOps, or other CI providers
- GitLab merge request integration (pipeline-on-MR detection)
- CircleCI insights or test metrics
- Flake detection for GitLab or CircleCI (is_flake always false)
- Webhook-based delivery (poll-only)
- Branch or tag filtering (all branches polled)
