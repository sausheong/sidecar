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
