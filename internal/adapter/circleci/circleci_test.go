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
