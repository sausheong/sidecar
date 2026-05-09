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
