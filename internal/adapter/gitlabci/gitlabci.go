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
