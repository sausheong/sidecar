package githubci

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
		slog.Warn("github-ci poll failed", "repo", a.repo, "err", err)
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
				"is_flake":      false,
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
