package uptime

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
)

const defaultPoll = 30 * time.Second

// UptimeAdapter polls a set of HTTP endpoints and emits SignalUptimeFailure
// when an endpoint is unreachable, returns an unexpected status code, or
// exceeds the configured latency threshold.
type UptimeAdapter struct {
	endpoints []config.UptimeEndpoint
	poll      time.Duration
	client    *http.Client
	stopCh    chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// New creates a UptimeAdapter from a signal config.
func New(sig config.SignalConfig) *UptimeAdapter {
	poll := sig.ParsedPollInterval()
	if sig.PollInterval == "" {
		poll = defaultPoll
	}
	timeout := 10 * time.Second
	for _, ep := range sig.Uptime.Endpoints {
		if d, err := time.ParseDuration(ep.Timeout); err == nil && d > timeout {
			timeout = d
		}
	}
	return &UptimeAdapter{
		endpoints: sig.Uptime.Endpoints,
		poll:      poll,
		client:    &http.Client{Timeout: timeout + 2*time.Second},
		stopCh:    make(chan struct{}),
	}
}

func (a *UptimeAdapter) Name() string { return "uptime" }

func (a *UptimeAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	if len(a.endpoints) == 0 {
		slog.Warn("uptime adapter: no endpoints configured, adapter is idle")
		return nil
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(a.poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.stopCh:
				return
			case <-ticker.C:
				a.checkAll(ctx, out)
			}
		}
	}()
	return nil
}

func (a *UptimeAdapter) Stop() error {
	a.stopOnce.Do(func() { close(a.stopCh) })
	a.wg.Wait()
	return nil
}

func (a *UptimeAdapter) checkAll(ctx context.Context, out chan<- adapter.Signal) {
	for _, ep := range a.endpoints {
		ep := ep
		sig, ok := a.check(ctx, ep)
		if !ok {
			continue
		}
		// Run diagnostics and attach results to the signal payload.
		diag := RunDiagnostics(ctx, ep, a.endpoints)
		sig.Payload["diagnostics"] = diag
		sig.Payload["diagnostic_summary"] = DiagnosticSummary(diag)
		select {
		case <-a.stopCh:
			return
		case out <- sig:
		}
	}
}

func (a *UptimeAdapter) check(ctx context.Context, ep config.UptimeEndpoint) (adapter.Signal, bool) {
	timeout := 10 * time.Second
	if d, err := time.ParseDuration(ep.Timeout); err == nil && d > 0 {
		timeout = d
	}
	expectStatus := ep.ExpectStatus
	if expectStatus == 0 {
		expectStatus = http.StatusOK
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ep.URL, nil)
	if err != nil {
		slog.Warn("uptime adapter: invalid endpoint URL", "url", ep.URL, "err", err)
		return adapter.Signal{}, false
	}

	start := time.Now()
	resp, err := a.client.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		return adapter.Signal{
			Type:   adapter.SignalUptimeFailure,
			Source: "uptime",
			Payload: map[string]any{
				"url":          ep.URL,
				"failure_type": "unreachable",
				"error":        err.Error(),
				"elapsed_ms":   elapsed.Milliseconds(),
			},
		}, true
	}
	resp.Body.Close()

	if resp.StatusCode != expectStatus {
		return adapter.Signal{
			Type:   adapter.SignalUptimeFailure,
			Source: "uptime",
			Payload: map[string]any{
				"url":             ep.URL,
				"failure_type":    "wrong_status",
				"got_status":      resp.StatusCode,
				"expected_status": expectStatus,
				"elapsed_ms":      elapsed.Milliseconds(),
			},
		}, true
	}

	if ep.ExpectMaxMs > 0 && elapsed.Milliseconds() > int64(ep.ExpectMaxMs) {
		return adapter.Signal{
			Type:   adapter.SignalUptimeFailure,
			Source: "uptime",
			Payload: map[string]any{
				"url":            ep.URL,
				"failure_type":   "slow_response",
				"elapsed_ms":     elapsed.Milliseconds(),
				"threshold_ms":   ep.ExpectMaxMs,
				"got_status":     resp.StatusCode,
			},
		}, true
	}

	return adapter.Signal{}, false
}

// FormatPayload returns a human-readable summary of an uptime failure payload.
func FormatPayload(p map[string]any) string {
	url, _ := p["url"].(string)
	ft, _ := p["failure_type"].(string)
	switch ft {
	case "unreachable":
		errMsg, _ := p["error"].(string)
		return fmt.Sprintf("%s is unreachable: %s", url, errMsg)
	case "wrong_status":
		got, _ := p["got_status"].(int)
		want, _ := p["expected_status"].(int)
		return fmt.Sprintf("%s returned %d, expected %d", url, got, want)
	case "slow_response":
		ms, _ := p["elapsed_ms"].(int64)
		threshold, _ := p["threshold_ms"].(int)
		return fmt.Sprintf("%s responded in %dms (threshold: %dms)", url, ms, threshold)
	default:
		return fmt.Sprintf("uptime check failed for %s", url)
	}
}
