# Sidecar Phase 7 — Additional CI Providers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add GitLab CI and CircleCI adapters to Sidecar, reusing `SignalCIFailure` and the existing adapter pattern, and generalize the hardcoded "GitHub Actions" references in prompt and triage functions.

**Architecture:** Two independent adapter packages (`internal/adapter/gitlabci/`, `internal/adapter/circleci/`) following `githubci` exactly. `BuildSystemPrompt` and `BuildTriageMessage` are updated to use `sig.Source` instead of hardcoding "GitHub Actions". CircleCI uses two API calls per unseen pipeline (pipeline list + workflow status).

**Tech Stack:** Go 1.25+, existing `Adapter` interface, `net/http` + `encoding/json`, `net/http/httptest` for tests, existing `SignalConfig.repo/token/watch/poll_interval` fields.

---

## File Map

```
internal/
  adapter/
    gitlabci/
      gitlabci.go       CREATE — GitLabCIAdapter
      gitlabci_test.go  CREATE — 3 httptest-based tests
    circleci/
      circleci.go       CREATE — CircleCIAdapter (two-call design)
      circleci_test.go  CREATE — 3 httptest-based tests
  loop/
    loop.go             MODIFY — generalize SignalCIFailure prompt (line ~305)
    loop_test.go        MODIFY — add TestBuildSystemPrompt_CIFailure_GitLab
  triage/
    triage.go           MODIFY — generalize SignalCIFailure triage message (line ~99)
    triage_test.go      MODIFY — add TestBuildTriageMessage_CIFailure_CircleCI
  cli/
    attach.go           MODIFY — add "gitlab-ci" and "circleci" cases to buildAdapters
```

---

### Task 1: Generalize CI Prompt and Triage Message

**Files:**
- Modify: `internal/loop/loop.go`
- Modify: `internal/loop/loop_test.go`
- Modify: `internal/triage/triage.go`
- Modify: `internal/triage/triage_test.go`

- [ ] **Step 1: Write the failing tests**

Read `internal/loop/loop_test.go`. Append:

```go
func TestBuildSystemPrompt_CIFailure_GitLab(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalCIFailure,
		Source: "gitlab-ci",
		Payload: map[string]any{
			"workflow_name": "main",
			"conclusion":    "failed",
			"head_sha":      "abc123",
			"html_url":      "https://gitlab.com/mygroup/myproject/-/pipelines/1",
		},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "gitlab-ci")
	assert.Contains(t, prompt, "main")
	assert.Contains(t, prompt, "abc123")
	assert.Contains(t, prompt, "engineering agent")
}
```

Read `internal/triage/triage_test.go`. Append:

```go
func TestBuildTriageMessage_CIFailure_CircleCI(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalCIFailure,
		Source: "circleci",
		Payload: map[string]any{
			"workflow_name": "build-and-test",
			"conclusion":    "failed",
			"head_sha":      "abc123",
			"html_url":      "https://app.circleci.com/pipelines/gh/org/repo/42",
			"repo":          "gh/org/repo",
			"is_flake":      false,
		},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "circleci")
	assert.Contains(t, msg, "build-and-test")
	assert.Contains(t, msg, "failed")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/loop/... ./internal/triage/... -v -run "TestBuildSystemPrompt_CIFailure_GitLab|TestBuildTriageMessage_CIFailure_CircleCI"
```

Expected: FAIL — `TestBuildSystemPrompt_CIFailure_GitLab` fails because prompt says "GitHub Actions" not "gitlab-ci"; `TestBuildTriageMessage_CIFailure_CircleCI` fails because message says "GitHub Actions" not "circleci".

- [ ] **Step 3: Update BuildSystemPrompt in loop.go**

Read `internal/loop/loop.go`. Find the `SignalCIFailure` case in `BuildSystemPrompt` (around line 299). Replace:

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

- [ ] **Step 4: Update BuildTriageMessage in triage.go**

Read `internal/triage/triage.go`. Find the `SignalCIFailure` case in `BuildTriageMessage` (around line 92). Replace:

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

- [ ] **Step 5: Run all loop and triage tests**

```bash
go test ./internal/loop/... ./internal/triage/... -v
```

Expected: all tests pass. The existing `TestBuildSystemPrompt_CIFailure` and `TestBuildTriageMessage_CIFailure` still pass because they assert on generic strings ("CI", "abc123") that remain present.

- [ ] **Step 6: Commit**

```bash
git add internal/loop/loop.go internal/loop/loop_test.go \
        internal/triage/triage.go internal/triage/triage_test.go
git commit -m "feat: generalize CI prompt and triage message to use sig.Source"
```

---

### Task 2: GitLabCIAdapter

**Files:**
- Create: `internal/adapter/gitlabci/gitlabci.go`
- Create: `internal/adapter/gitlabci/gitlabci_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/gitlabci/gitlabci_test.go`:

```go
package gitlabci_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/adapter/gitlabci"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockPipelinesResponse(pipelines []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pipelines)
	}
}

func TestGitLabCIAdapter_DetectsFailure(t *testing.T) {
	server := httptest.NewServer(mockPipelinesResponse([]map[string]any{
		{
			"id":      int64(1001),
			"status":  "failed",
			"ref":     "main",
			"sha":     "abc123",
			"web_url": "https://gitlab.com/mygroup/myproject/-/pipelines/1001",
		},
	}))
	defer server.Close()

	a := gitlabci.NewWithBaseURL("mygroup/myproject", "token", 100*time.Millisecond, []string{"failed"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalCIFailure, sig.Type)
		assert.Equal(t, "gitlab-ci", sig.Source)
		assert.Equal(t, int64(1001), sig.Payload["pipeline_id"])
		assert.Equal(t, "main", sig.Payload["workflow_name"])
		assert.Equal(t, "failed", sig.Payload["conclusion"])
		assert.Equal(t, "abc123", sig.Payload["head_sha"])
		assert.Equal(t, "mygroup/myproject", sig.Payload["repo"])
	case <-ctx.Done():
		t.Fatal("no signal received within timeout")
	}
}

func TestGitLabCIAdapter_NoDuplicates(t *testing.T) {
	server := httptest.NewServer(mockPipelinesResponse([]map[string]any{
		{"id": int64(1001), "status": "failed", "ref": "main", "sha": "abc",
			"web_url": "https://gitlab.com/mygroup/myproject/-/pipelines/1001"},
		{"id": int64(1002), "status": "failed", "ref": "main", "sha": "def",
			"web_url": "https://gitlab.com/mygroup/myproject/-/pipelines/1002"},
	}))
	defer server.Close()

	a := gitlabci.NewWithBaseURL("mygroup/myproject", "token", 100*time.Millisecond, []string{"failed"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	for i := 0; i < 2; i++ {
		select {
		case <-signals:
		case <-ctx.Done():
			t.Fatal("timed out waiting for initial signals")
		}
	}

	time.Sleep(350 * time.Millisecond)
	assert.Empty(t, signals, "duplicate pipeline IDs should not re-fire")
}

func TestGitLabCIAdapter_IgnoresNonWatchedStatus(t *testing.T) {
	server := httptest.NewServer(mockPipelinesResponse([]map[string]any{
		{"id": int64(2001), "status": "success", "ref": "main", "sha": "xyz",
			"web_url": "https://gitlab.com/mygroup/myproject/-/pipelines/2001"},
	}))
	defer server.Close()

	a := gitlabci.NewWithBaseURL("mygroup/myproject", "token", 100*time.Millisecond, []string{"failed"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(350 * time.Millisecond)
	assert.Empty(t, signals, "successful pipelines should not fire when only 'failed' is watched")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/gitlabci/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement gitlabci.go**

Create `internal/adapter/gitlabci/gitlabci.go`:

```go
package gitlabci

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
)

const defaultBaseURL = "https://gitlab.com"

// GitLabCIAdapter polls GitLab pipelines for failures.
type GitLabCIAdapter struct {
	repo         string // "namespace/project", e.g. "mygroup/myproject"
	token        string
	pollInterval time.Duration
	watch        []string // pipeline statuses to act on
	baseURL      string

	seen     map[int64]bool
	seenMu   sync.Mutex
	stopOnce sync.Once
	stopCh   chan struct{}
	client   *http.Client
}

// New creates a GitLabCIAdapter using the production GitLab API.
func New(repo, token string, pollInterval time.Duration, watch []string) *GitLabCIAdapter {
	return NewWithBaseURL(repo, token, pollInterval, watch, defaultBaseURL)
}

// NewWithBaseURL creates a GitLabCIAdapter with a custom base URL (used in tests).
func NewWithBaseURL(repo, token string, pollInterval time.Duration, watch []string, baseURL string) *GitLabCIAdapter {
	return &GitLabCIAdapter{
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

func (a *GitLabCIAdapter) Name() string { return "gitlab-ci" }

func (a *GitLabCIAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
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

func (a *GitLabCIAdapter) Stop() error {
	a.stopOnce.Do(func() { close(a.stopCh) })
	return nil
}

type gitlabPipeline struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Ref    string `json:"ref"`
	SHA    string `json:"sha"`
	WebURL string `json:"web_url"`
}

func (a *GitLabCIAdapter) poll(ctx context.Context, out chan<- adapter.Signal) {
	pipelines, err := a.fetchPipelines(ctx)
	if err != nil {
		slog.Warn("gitlab-ci poll failed", "repo", a.repo, "err", err)
		return
	}
	for _, p := range pipelines {
		if !a.isWatched(p.Status) {
			continue
		}
		a.seenMu.Lock()
		already := a.seen[p.ID]
		if !already {
			a.seen[p.ID] = true
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
			Source: "gitlab-ci",
			Payload: map[string]any{
				"pipeline_id":   p.ID,
				"workflow_name": p.Ref,
				"conclusion":    p.Status,
				"html_url":      p.WebURL,
				"head_sha":      p.SHA,
				"repo":          a.repo,
				"is_flake":      false,
			},
		}:
		}
	}
}

func (a *GitLabCIAdapter) fetchPipelines(ctx context.Context) ([]gitlabPipeline, error) {
	encoded := url.PathEscape(a.repo)
	apiURL := fmt.Sprintf("%s/api/v4/projects/%s/pipelines?order_by=id&sort=desc&per_page=10",
		a.baseURL, encoded)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	if a.token != "" {
		req.Header.Set("PRIVATE-TOKEN", a.token)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitlab api status %d", resp.StatusCode)
	}

	var pipelines []gitlabPipeline
	if err := json.NewDecoder(resp.Body).Decode(&pipelines); err != nil {
		return nil, err
	}
	return pipelines, nil
}

func (a *GitLabCIAdapter) isWatched(status string) bool {
	for _, w := range a.watch {
		if w == status {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run all GitLab CI tests**

```bash
go test ./internal/adapter/gitlabci/... -v
```

Expected: all 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/gitlabci/gitlabci.go internal/adapter/gitlabci/gitlabci_test.go
git commit -m "feat: GitLabCIAdapter — polls pipelines API for failed/canceled pipelines"
```

---

### Task 3: CircleCIAdapter

**Files:**
- Create: `internal/adapter/circleci/circleci.go`
- Create: `internal/adapter/circleci/circleci_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/circleci/circleci_test.go`:

```go
package circleci_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/adapter/circleci"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCircleCIServer mocks both the pipeline list endpoint and the workflow endpoint.
// Requests containing "/workflow" receive the workflows response; all others receive the pipelines response.
func newCircleCIServer(pipelines []map[string]any, workflows []map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/workflow") {
			json.NewEncoder(w).Encode(map[string]any{"items": workflows})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"items": pipelines})
		}
	}))
}

func TestCircleCIAdapter_DetectsFailure(t *testing.T) {
	server := newCircleCIServer(
		[]map[string]any{
			{"id": "uuid-1001", "number": int64(42), "vcs": map[string]any{"revision": "abc123"}},
		},
		[]map[string]any{
			{"name": "build-and-test", "status": "failed"},
		},
	)
	defer server.Close()

	a := circleci.NewWithBaseURL("gh/myorg/myrepo", "token", 100*time.Millisecond, []string{"failed"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalCIFailure, sig.Type)
		assert.Equal(t, "circleci", sig.Source)
		assert.Equal(t, int64(42), sig.Payload["pipeline_id"])
		assert.Equal(t, "build-and-test", sig.Payload["workflow_name"])
		assert.Equal(t, "failed", sig.Payload["conclusion"])
		assert.Equal(t, "abc123", sig.Payload["head_sha"])
		assert.Equal(t, "gh/myorg/myrepo", sig.Payload["repo"])
	case <-ctx.Done():
		t.Fatal("no signal received within timeout")
	}
}

func TestCircleCIAdapter_NoDuplicates(t *testing.T) {
	server := newCircleCIServer(
		[]map[string]any{
			{"id": "uuid-1001", "number": int64(42), "vcs": map[string]any{"revision": "abc"}},
		},
		[]map[string]any{
			{"name": "build", "status": "failed"},
		},
	)
	defer server.Close()

	a := circleci.NewWithBaseURL("gh/myorg/myrepo", "token", 100*time.Millisecond, []string{"failed"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case <-signals: // first trigger
	case <-ctx.Done():
		t.Fatal("no signal received")
	}

	time.Sleep(350 * time.Millisecond)
	assert.Empty(t, signals, "same pipeline UUID should not re-fire")
}

func TestCircleCIAdapter_IgnoresNonWatchedStatus(t *testing.T) {
	server := newCircleCIServer(
		[]map[string]any{
			{"id": "uuid-2001", "number": int64(99), "vcs": map[string]any{"revision": "xyz"}},
		},
		[]map[string]any{
			{"name": "build", "status": "success"},
		},
	)
	defer server.Close()

	a := circleci.NewWithBaseURL("gh/myorg/myrepo", "token", 100*time.Millisecond, []string{"failed"}, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(350 * time.Millisecond)
	assert.Empty(t, signals, "successful workflows should not fire when only 'failed' is watched")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/circleci/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement circleci.go**

Create `internal/adapter/circleci/circleci.go`:

```go
package circleci

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
)

const defaultBaseURL = "https://circleci.com"

// CircleCIAdapter polls CircleCI pipelines and their workflows for failures.
type CircleCIAdapter struct {
	repo         string // project slug, e.g. "gh/myorg/myrepo"
	token        string
	pollInterval time.Duration
	watch        []string // workflow statuses to act on: "failed", "error"
	baseURL      string

	seen     map[string]bool // keyed by pipeline UUID string
	seenMu   sync.Mutex
	stopOnce sync.Once
	stopCh   chan struct{}
	client   *http.Client
}

// New creates a CircleCIAdapter using the production CircleCI API.
func New(repo, token string, pollInterval time.Duration, watch []string) *CircleCIAdapter {
	return NewWithBaseURL(repo, token, pollInterval, watch, defaultBaseURL)
}

// NewWithBaseURL creates a CircleCIAdapter with a custom base URL (used in tests).
func NewWithBaseURL(repo, token string, pollInterval time.Duration, watch []string, baseURL string) *CircleCIAdapter {
	return &CircleCIAdapter{
		repo:         repo,
		token:        token,
		pollInterval: pollInterval,
		watch:        watch,
		baseURL:      baseURL,
		seen:         make(map[string]bool),
		stopCh:       make(chan struct{}),
		client:       &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *CircleCIAdapter) Name() string { return "circleci" }

func (a *CircleCIAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
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

func (a *CircleCIAdapter) Stop() error {
	a.stopOnce.Do(func() { close(a.stopCh) })
	return nil
}

type circlePipeline struct {
	ID     string `json:"id"`
	Number int64  `json:"number"`
	VCS    struct {
		Revision string `json:"revision"`
	} `json:"vcs"`
}

type circleWorkflow struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (a *CircleCIAdapter) poll(ctx context.Context, out chan<- adapter.Signal) {
	pipelines, err := a.fetchPipelines(ctx)
	if err != nil {
		slog.Warn("circleci poll failed", "repo", a.repo, "err", err)
		return
	}
	for _, p := range pipelines {
		a.seenMu.Lock()
		already := a.seen[p.ID]
		if !already {
			a.seen[p.ID] = true
		}
		a.seenMu.Unlock()
		if already {
			continue
		}

		workflows, err := a.fetchWorkflows(ctx, p.ID)
		if err != nil {
			slog.Warn("circleci workflow fetch failed", "pipeline", p.ID, "err", err)
			continue
		}

		for _, wf := range workflows {
			if !a.isWatched(wf.Status) {
				continue
			}
			select {
			case <-a.stopCh:
				return
			case out <- adapter.Signal{
				Type:   adapter.SignalCIFailure,
				Source: "circleci",
				Payload: map[string]any{
					"pipeline_id":   p.Number,
					"workflow_name": wf.Name,
					"conclusion":    wf.Status,
					"html_url":      fmt.Sprintf("https://app.circleci.com/pipelines/%s/%d", a.repo, p.Number),
					"head_sha":      p.VCS.Revision,
					"repo":          a.repo,
					"is_flake":      false,
				},
			}:
			}
			break // first matching workflow only
		}
	}
}

func (a *CircleCIAdapter) fetchPipelines(ctx context.Context) ([]circlePipeline, error) {
	apiURL := fmt.Sprintf("%s/api/v2/project/%s/pipeline?limit=10", a.baseURL, a.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	if a.token != "" {
		req.Header.Set("Circle-Token", a.token)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("circleci api status %d", resp.StatusCode)
	}

	var body struct {
		Items []circlePipeline `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Items, nil
}

func (a *CircleCIAdapter) fetchWorkflows(ctx context.Context, pipelineID string) ([]circleWorkflow, error) {
	apiURL := fmt.Sprintf("%s/api/v2/pipeline/%s/workflow", a.baseURL, pipelineID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	if a.token != "" {
		req.Header.Set("Circle-Token", a.token)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("circleci workflow api status %d", resp.StatusCode)
	}

	var body struct {
		Items []circleWorkflow `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Items, nil
}

func (a *CircleCIAdapter) isWatched(status string) bool {
	for _, w := range a.watch {
		if w == status {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run all CircleCI tests**

```bash
go test ./internal/adapter/circleci/... -v
```

Expected: all 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/circleci/circleci.go internal/adapter/circleci/circleci_test.go
git commit -m "feat: CircleCIAdapter — polls pipelines and workflows API for failures"
```

---

### Task 4: CLI Wiring and Final Verification

**Files:**
- Modify: `internal/cli/attach.go`

- [ ] **Step 1: Wire the new adapters**

Read `internal/cli/attach.go`. Add the two imports alongside existing adapter imports:

```go
gitlabciadapter "github.com/sausheong/sidecar/internal/adapter/gitlabci"
circleciadapter "github.com/sausheong/sidecar/internal/adapter/circleci"
```

Add two cases to `buildAdapters` switch (after `case "github-ci":`):

```go
case "gitlab-ci":
	token := sig.ResolveToken()
	interval := sig.ParsedPollInterval()
	adapters = append(adapters, gitlabciadapter.New(sig.Repo, token, interval, sig.Watch))

case "circleci":
	token := sig.ResolveToken()
	interval := sig.ParsedPollInterval()
	adapters = append(adapters, circleciadapter.New(sig.Repo, token, interval, sig.Watch))
```

- [ ] **Step 2: Build and verify**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 4: Build binary and verify help**

```bash
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar --help
```

Expected: help output shows `attach`, `task`, `status`, `ask` commands.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/attach.go
git commit -m "feat: wire gitlab-ci and circleci adapters into sidecar attach"
```

---

## Verification

After all tasks complete:

```bash
go test ./... 2>&1 | grep -v "^?"
```

Expected: all packages pass.

To exercise GitLab CI end-to-end (requires real credentials):

```yaml
# sidecar.yaml
signals:
  - adapter: gitlab-ci
    repo: "mygroup/myproject"
    token: $GITLAB_TOKEN
    poll_interval: "60s"
    watch:
      - "failed"
      - "canceled"
```

To exercise CircleCI end-to-end:

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
