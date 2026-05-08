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
			"id":          int64(1001),
			"name":        "CI",
			"conclusion":  "failure",
			"html_url":    "https://github.com/org/repo/actions/runs/1001",
			"head_sha":    "abc123",
			"workflow_id": int64(42),
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
