package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
)

// Alert is a single firing alert returned by a MetricsProvider.
type Alert struct {
	ID      string
	Name    string
	Message string
	Labels  map[string]string
}

// MetricsProvider polls a metrics backend for currently firing alerts.
type MetricsProvider interface {
	FiringAlerts(ctx context.Context) ([]Alert, error)
}

// MetricsAdapter polls a MetricsProvider and emits SignalMetricAlert for newly-firing alerts.
type MetricsAdapter struct {
	provider     MetricsProvider
	providerName string
	pollInterval time.Duration
	seen         map[string]bool
	seenMu       sync.Mutex
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

// NewMetricsAdapter constructs a MetricsAdapter from a signal config.
func NewMetricsAdapter(sig config.SignalConfig) (*MetricsAdapter, error) {
	poll := sig.ParsedPollInterval()
	switch sig.Metrics.Provider {
	case "datadog":
		p := NewDatadogProviderWithBaseURL(
			sig.ResolveToken(),
			os.Getenv("DATADOG_APP_KEY"),
			sig.Metrics.Tags,
			sig.Metrics.AlertNames,
			&http.Client{Timeout: 15 * time.Second},
			datadogBaseURL,
		)
		return newAdapter(p, "datadog", poll), nil
	case "prometheus":
		if sig.Metrics.Endpoint == "" {
			return nil, fmt.Errorf("metrics adapter: prometheus requires a non-empty endpoint")
		}
		p := NewPrometheusProvider(sig.Metrics.Endpoint, sig.Metrics.AlertNames,
			&http.Client{Timeout: 15 * time.Second})
		return newAdapter(p, "prometheus", poll), nil
	default:
		return nil, fmt.Errorf("metrics adapter: unknown provider %q (want \"datadog\" or \"prometheus\")",
			sig.Metrics.Provider)
	}
}

// NewMetricsAdapterWithProvider creates a MetricsAdapter with a specific provider. Used in tests.
func NewMetricsAdapterWithProvider(provider MetricsProvider, providerName string, pollInterval time.Duration) *MetricsAdapter {
	return newAdapter(provider, providerName, pollInterval)
}

func newAdapter(provider MetricsProvider, providerName string, pollInterval time.Duration) *MetricsAdapter {
	return &MetricsAdapter{
		provider:     provider,
		providerName: providerName,
		pollInterval: pollInterval,
		seen:         make(map[string]bool),
		stopCh:       make(chan struct{}),
	}
}

func (a *MetricsAdapter) Name() string { return "metrics" }

// Start launches a goroutine that polls for firing alerts on each tick.
func (a *MetricsAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
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

// Stop closes the stop channel and waits for the poll goroutine to exit.
func (a *MetricsAdapter) Stop() error {
	a.stopOnce.Do(func() { close(a.stopCh) })
	a.wg.Wait()
	return nil
}

// poll fetches firing alerts, emits signals for newly-firing ones, and re-arms resolved ones.
func (a *MetricsAdapter) poll(ctx context.Context, out chan<- adapter.Signal) {
	alerts, err := a.provider.FiringAlerts(ctx)
	if err != nil {
		slog.Warn("metrics adapter: poll failed", "provider", a.providerName, "err", err)
		return
	}

	current := make(map[string]bool, len(alerts))
	for _, al := range alerts {
		current[al.ID] = true
	}

	var toSend []adapter.Signal

	a.seenMu.Lock()
	for _, al := range alerts {
		if !a.seen[al.ID] {
			a.seen[al.ID] = true
			toSend = append(toSend, adapter.Signal{
				Type:   adapter.SignalMetricAlert,
				Source: "metrics",
				Payload: map[string]any{
					"alert_id":   al.ID,
					"alert_name": al.Name,
					"message":    al.Message,
					"labels":     al.Labels,
					"provider":   a.providerName,
				},
			})
		}
	}
	for id := range a.seen {
		if !current[id] {
			delete(a.seen, id)
		}
	}
	a.seenMu.Unlock()

	for _, sig := range toSend {
		select {
		case <-a.stopCh:
			return
		case out <- sig:
		}
	}
}
