package notify_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestEventBudgetExceededConstant(t *testing.T) {
	assert.Equal(t, notify.Event("budget_exceeded"), notify.EventBudgetExceeded)
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

// ── Email notifier ────────────────────────────────────────────────────────────

// smtpServer spins up a minimal SMTP server on a random port that collects
// the raw DATA payload for each message received.
func smtpServer(t *testing.T) (addr string, messages func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var received []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var buf strings.Builder
				sc := bufio.NewScanner(c)
				fmt.Fprintf(c, "220 test ESMTP\r\n")
				inData := false
				for sc.Scan() {
					line := sc.Text()
					if inData {
						if line == "." {
							received = append(received, buf.String())
							buf.Reset()
							inData = false
							fmt.Fprintf(c, "250 OK\r\n")
						} else {
							buf.WriteString(line + "\n")
						}
						continue
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						fmt.Fprintf(c, "250 OK\r\n")
					case strings.HasPrefix(line, "MAIL FROM"):
						fmt.Fprintf(c, "250 OK\r\n")
					case strings.HasPrefix(line, "RCPT TO"):
						fmt.Fprintf(c, "250 OK\r\n")
					case line == "DATA":
						fmt.Fprintf(c, "354 Start\r\n")
						inData = true
					case strings.HasPrefix(line, "QUIT"):
						fmt.Fprintf(c, "221 Bye\r\n")
						return
					default:
						fmt.Fprintf(c, "250 OK\r\n")
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), func() []string { return received }
}

func TestEmailNotifier_SendsMessage(t *testing.T) {
	addr, messages := smtpServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	cfg := config.EmailConfig{
		SMTPHost: host,
		SMTPPort: port,
		From:     "sidecar@example.com",
		To:       []string{"oncall@example.com"},
	}
	n := notify.NewEmailNotifier(cfg)
	sig := testSig("wrong_status")
	sig.Payload["got_status"] = 503
	sig.Payload["expected_status"] = 200
	task := testTask("fix uptime: /health (wrong_status)")

	err := n.Notify(context.Background(), notify.EventNotified, sig, task)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	msgs := messages()
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], "Sidecar")
	assert.Contains(t, msgs[0], "notified")
	assert.Contains(t, msgs[0], "uptime.failure")
}

func TestEmailNotifier_NoRecipients(t *testing.T) {
	cfg := config.EmailConfig{SMTPHost: "localhost", SMTPPort: 1025}
	n := notify.NewEmailNotifier(cfg)
	err := n.Notify(context.Background(), notify.EventFailed, testSig("unreachable"), testTask("t"))
	assert.Error(t, err)
}

func TestDispatcher_EmailSkippedWhenNoHost(t *testing.T) {
	d := notify.NewDispatcher([]config.NotificationConfig{
		{Provider: "email", On: []string{"failed"}}, // no smtp_host
	})
	// Dispatcher should have silently skipped the misconfigured notifier
	assert.False(t, d.Enabled())
}

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
