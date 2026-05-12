package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/notify"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTask(summary string) *store.Task {
	return &store.Task{
		ID:        uuid.New(),
		Summary:   summary,
		CreatedAt: time.Now(),
	}
}

func testSig(ft string) adapter.Signal {
	return adapter.Signal{
		Type:   adapter.SignalUptimeFailure,
		Source: "uptime",
		Payload: map[string]any{
			"url":          "https://api.example.com/health",
			"failure_type": ft,
			"got_status":   503,
		},
	}
}

// ── Dispatcher ────────────────────────────────────────────────────────────────

func TestDispatcher_NilSafe(t *testing.T) {
	var d *notify.Dispatcher
	// Must not panic
	d.Fire(context.Background(), notify.EventSkipped, testSig("wrong_status"), testTask("t"))
	assert.False(t, d.Enabled())
}

func TestDispatcher_NoConfig(t *testing.T) {
	d := notify.NewDispatcher(nil)
	assert.False(t, d.Enabled())
}

func TestDispatcher_FiltersEvents(t *testing.T) {
	var received []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		received = append(received, body["event"].(string))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := notify.NewDispatcher([]config.NotificationConfig{
		{Provider: "webhook", URL: srv.URL, On: []string{"failed", "completed"}},
	})
	require.True(t, d.Enabled())

	ctx := context.Background()
	sig := testSig("unreachable")
	task := testTask("check health")

	d.Fire(ctx, notify.EventSkipped, sig, task)   // not subscribed
	d.Fire(ctx, notify.EventFailed, sig, task)    // subscribed
	d.Fire(ctx, notify.EventCompleted, sig, task) // subscribed

	assert.Equal(t, []string{"failed", "completed"}, received)
}

// ── Webhook notifier ──────────────────────────────────────────────────────────

func TestWebhookNotifier_SendsJSON(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.NewWebhookNotifier(srv.URL)
	sig := testSig("wrong_status")
	task := testTask("uptime check")

	err := n.Notify(context.Background(), notify.EventNotified, sig, task)
	require.NoError(t, err)

	assert.Equal(t, "notified", got["event"])
	assert.Equal(t, "uptime.failure", got["signal_type"])
	assert.Equal(t, "uptime check", got["summary"])
	assert.NotEmpty(t, got["task_id"])
}

func TestWebhookNotifier_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := notify.NewWebhookNotifier(srv.URL)
	err := n.Notify(context.Background(), notify.EventFailed, testSig("unreachable"), testTask("t"))
	assert.Error(t, err)
}

// ── Slack notifier ────────────────────────────────────────────────────────────

func TestSlackNotifier_SendsBlocks(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.NewSlackNotifier(srv.URL)
	sig := testSig("slow_response")
	sig.Payload["elapsed_ms"] = int64(800)
	sig.Payload["threshold_ms"] = 500

	err := n.Notify(context.Background(), notify.EventSuggested, sig, testTask("perf check"))
	require.NoError(t, err)

	blocks, ok := got["blocks"].([]any)
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(blocks), 2)
}

// ── Dispatcher with Slack (via test server) ───────────────────────────────────

func TestDispatcher_MultipleNotifiers(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := notify.NewDispatcher([]config.NotificationConfig{
		{Provider: "webhook", URL: srv.URL, On: []string{"notified"}},
		{Provider: "webhook", URL: srv.URL, On: []string{"notified"}},
	})

	d.Fire(context.Background(), notify.EventNotified, testSig("unreachable"), testTask("t"))
	assert.Equal(t, 2, calls, "both notifiers should fire")
}
