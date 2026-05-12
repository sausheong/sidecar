// Package notify dispatches event notifications to configured channels
// (Slack, generic webhooks) when sidecar tasks reach notable states.
package notify

import (
	"context"
	"log/slog"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/store"
)

// Event is a task lifecycle milestone that can trigger a notification.
type Event string

const (
	EventSkipped   Event = "skipped"   // triage decided not to act
	EventSuggested Event = "suggested" // agent ran; suggest-only output recorded
	EventCompleted Event = "completed" // fix committed or PR opened
	EventFailed    Event = "failed"    // agent or loop error
	EventNotified  Event = "notified"  // notify autonomy level — no agent run
)

// Notifier sends a notification for a task event.
type Notifier interface {
	// Name returns the notifier identifier for logging.
	Name() string
	// Notify sends the notification. It must not block for more than a few seconds.
	Notify(ctx context.Context, ev Event, sig adapter.Signal, task *store.Task) error
}

// Dispatcher holds a set of Notifiers and their event filters, and
// fans out events to all notifiers that subscribe to that event.
type Dispatcher struct {
	entries []dispatchEntry
}

type dispatchEntry struct {
	notifier Notifier
	on       map[Event]bool
}

// NewDispatcher builds a Dispatcher from the notifications config.
func NewDispatcher(cfgs []config.NotificationConfig) *Dispatcher {
	d := &Dispatcher{}
	for _, nc := range cfgs {
		n := buildNotifier(nc)
		if n == nil {
			continue
		}
		on := make(map[Event]bool, len(nc.On))
		for _, ev := range nc.On {
			on[Event(ev)] = true
		}
		d.entries = append(d.entries, dispatchEntry{notifier: n, on: on})
	}
	return d
}

func buildNotifier(nc config.NotificationConfig) Notifier {
	switch nc.Provider {
	case "slack":
		url := nc.ResolveWebhook()
		if url == "" {
			slog.Warn("notify: slack notifier has empty webhook URL, skipping")
			return nil
		}
		return NewSlackNotifier(url)
	case "webhook":
		url := nc.ResolveURL()
		if url == "" {
			slog.Warn("notify: webhook notifier has empty URL, skipping")
			return nil
		}
		return NewWebhookNotifier(url)
	default:
		slog.Warn("notify: unknown provider, skipping", "provider", nc.Provider)
		return nil
	}
}

// Fire sends ev to every notifier subscribed to that event.
// Errors are logged but never propagated — notifications are best-effort.
func (d *Dispatcher) Fire(ctx context.Context, ev Event, sig adapter.Signal, task *store.Task) {
	if d == nil {
		return
	}
	for _, e := range d.entries {
		if !e.on[ev] {
			continue
		}
		if err := e.notifier.Notify(ctx, ev, sig, task); err != nil {
			slog.Warn("notify: send failed", "provider", e.notifier.Name(), "event", ev, "err", err)
		}
	}
}

// Enabled reports whether the dispatcher has at least one notifier configured.
func (d *Dispatcher) Enabled() bool {
	return d != nil && len(d.entries) > 0
}
