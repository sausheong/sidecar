# Sidecar Phase 6 — Metrics Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `metrics` adapter that polls Datadog and Prometheus for firing alerts and emits `SignalMetricAlert` into the existing triage → improve loop.

**Architecture:** Pluggable `MetricsProvider` interface with Datadog and Prometheus implementations, all co-located in `internal/adapter/metrics/`. Edge-triggered deduplication via a `seen map[string]bool` — same pattern as the GitHub CI adapter. New `metric_fix` change type and `metric_fixes` autonomy level wired through triage, loop, and CLI.

**Tech Stack:** Go 1.25+, existing `Adapter` interface (`Name/Start/Stop`), `net/http` + `encoding/json` for API calls, `net/http/httptest` for tests, existing cobra CLI pattern.

---

## File Map

```
internal/
  adapter/
    adapter.go                  MODIFY — add SignalMetricAlert constant
    metrics/
      metrics.go                CREATE — MetricsAdapter, MetricsProvider interface, Alert type, constructors
      metrics_test.go           CREATE — adapter tests via mock provider
      datadog.go                CREATE — DatadogProvider (Monitors API)
      datadog_test.go           CREATE — httptest-based unit tests
      prometheus.go             CREATE — PrometheusProvider (Rules API)
      prometheus_test.go        CREATE — httptest-based unit tests
  config/
    config.go                   MODIFY — MetricsSignalConfig, MetricFixes in AutonomyPolicy
    metrics_config_test.go      CREATE — unmarshal tests for new config types
  loop/
    loop.go                     MODIFY — SignalMetricAlert in BuildSystemPrompt, userMessage, summarize
    loop_test.go                MODIFY — TestBuildSystemPrompt_MetricAlert
  triage/
    triage.go                   MODIFY — "metric_fix" in triageSystemPrompt, BuildTriageMessage, ResolveAutonomy
    triage_test.go              MODIFY — TestBuildTriageMessage_MetricAlert, TestResolveAutonomy_MetricFix
  cli/
    attach.go                   MODIFY — case "metrics" in buildAdapters
```

---

### Task 1: Config Types

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/metrics_config_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/config/metrics_config_test.go`:

```go
package config_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestMetricsSignalConfig_Datadog(t *testing.T) {
	const data = `
adapter: metrics
poll_interval: "60s"
token: $DATADOG_API_KEY
metrics:
  provider: datadog
  tags:
    - "env:production"
  alert_names:
    - "High Error Rate"
`
	var sig config.SignalConfig
	require.NoError(t, yaml.Unmarshal([]byte(data), &sig))
	assert.Equal(t, "metrics", sig.Adapter)
	assert.Equal(t, "datadog", sig.Metrics.Provider)
	assert.Equal(t, []string{"env:production"}, sig.Metrics.Tags)
	assert.Equal(t, []string{"High Error Rate"}, sig.Metrics.AlertNames)
}

func TestMetricsSignalConfig_Prometheus(t *testing.T) {
	const data = `
adapter: metrics
metrics:
  provider: prometheus
  endpoint: "http://localhost:9090"
`
	var sig config.SignalConfig
	require.NoError(t, yaml.Unmarshal([]byte(data), &sig))
	assert.Equal(t, "prometheus", sig.Metrics.Provider)
	assert.Equal(t, "http://localhost:9090", sig.Metrics.Endpoint)
}

func TestAutonomyPolicy_MetricFixes(t *testing.T) {
	const data = `
autonomy:
  metric_fixes: suggest-only
`
	var cfg config.Config
	require.NoError(t, yaml.Unmarshal([]byte(data), &cfg))
	assert.Equal(t, "suggest-only", cfg.Autonomy.MetricFixes)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/config/... -v -run "TestMetricsSignalConfig|TestAutonomyPolicy_MetricFixes"
```

Expected: FAIL — `config.SignalConfig` has no field `Metrics`.

- [ ] **Step 3: Add types to config.go**

Read `internal/config/config.go`. Add after the `LogRateConfig` type (around line 44):

```go
type MetricsSignalConfig struct {
	Provider   string   `yaml:"provider"`    // "datadog" | "prometheus"
	Endpoint   string   `yaml:"endpoint"`    // Prometheus base URL, e.g. "http://localhost:9090"
	Tags       []string `yaml:"tags"`        // Datadog: filter monitors by tags
	AlertNames []string `yaml:"alert_names"` // optional allowlist; empty = all alerts
}
```

Add `Metrics MetricsSignalConfig` field to `SignalConfig` (after the `Logs` field):

```go
type SignalConfig struct {
	Adapter      string              `yaml:"adapter"`
	Watch        []string            `yaml:"watch"`
	Cron         string              `yaml:"cron"`
	Repo         string              `yaml:"repo"`
	Token        string              `yaml:"token"`
	PollInterval string              `yaml:"poll_interval"`
	Logs         LogsSignalConfig    `yaml:"logs"`
	Metrics      MetricsSignalConfig `yaml:"metrics"`
}
```

Add `MetricFixes` to `AutonomyPolicy`:

```go
type AutonomyPolicy struct {
	DependencyUpdates string `yaml:"dependency_updates"`
	TestFixes         string `yaml:"test_fixes"`
	BugFixes          string `yaml:"bug_fixes"`
	Refactoring       string `yaml:"refactoring"`
	SchemaChanges     string `yaml:"schema_changes"`
	LogFixes          string `yaml:"log_fixes"`
	MetricFixes       string `yaml:"metric_fixes"`
}
```

- [ ] **Step 4: Run all config tests**

```bash
go test ./internal/config/... -v
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/metrics_config_test.go
git commit -m "feat: add MetricsSignalConfig types and MetricFixes autonomy field"
```

---

### Task 2: Signal Type Constant

**Files:**
- Modify: `internal/adapter/adapter.go`

- [ ] **Step 1: Add the constant**

Read `internal/adapter/adapter.go`. Add `SignalMetricAlert` to the existing const block:

```go
const (
	SignalGitCommit    SignalType = "git.commit"
	SignalScheduleTick SignalType = "schedule.tick"
	SignalOnDemand     SignalType = "ondemand.task"
	SignalCIFailure    SignalType = "ci.failure"
	SignalLogAnomaly   SignalType = "log.anomaly"
	SignalMetricAlert  SignalType = "metric.alert"
)
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/adapter/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/adapter/adapter.go
git commit -m "feat: add SignalMetricAlert signal type constant"
```

---

### Task 3: MetricsAdapter, Interface, and Alert Type

**Files:**
- Create: `internal/adapter/metrics/metrics.go`
- Create: `internal/adapter/metrics/metrics_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/metrics/metrics_test.go`:

```go
package metrics_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	metricsadapter "github.com/sausheong/sidecar/internal/adapter/metrics"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockProvider struct {
	mu     sync.Mutex
	alerts []metricsadapter.Alert
	err    error
}

func (m *mockProvider) FiringAlerts(_ context.Context) ([]metricsadapter.Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alerts, m.err
}

func (m *mockProvider) set(alerts []metricsadapter.Alert) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = alerts
}

func makeAdapter(p metricsadapter.MetricsProvider) *metricsadapter.MetricsAdapter {
	return metricsadapter.NewMetricsAdapterWithProvider(p, "mock", 50*time.Millisecond)
}

func TestMetricsAdapter_Name(t *testing.T) {
	a := makeAdapter(&mockProvider{})
	assert.Equal(t, "metrics", a.Name())
}

func TestMetricsAdapter_NewAlertEmitsSignal(t *testing.T) {
	mock := &mockProvider{alerts: []metricsadapter.Alert{
		{ID: "alert-1", Name: "High CPU", Message: "cpu > 90%", Labels: map[string]string{"env": "prod"}},
	}}
	a := makeAdapter(mock)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalMetricAlert, sig.Type)
		assert.Equal(t, "metrics", sig.Source)
		assert.Equal(t, "alert-1", sig.Payload["alert_id"])
		assert.Equal(t, "High CPU", sig.Payload["alert_name"])
		assert.Equal(t, "mock", sig.Payload["provider"])
	case <-ctx.Done():
		t.Fatal("no signal received")
	}
}

func TestMetricsAdapter_NoDoubleFire(t *testing.T) {
	mock := &mockProvider{alerts: []metricsadapter.Alert{
		{ID: "alert-1", Name: "High CPU", Message: "cpu > 90%"},
	}}
	a := makeAdapter(mock)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, 1, len(signals), "same alert must not fire more than once while still firing")
}

func TestMetricsAdapter_RearmOnResolve(t *testing.T) {
	mock := &mockProvider{alerts: []metricsadapter.Alert{
		{ID: "alert-1", Name: "High CPU", Message: "cpu > 90%"},
	}}
	a := makeAdapter(mock)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case <-signals: // first trigger
	case <-ctx.Done():
		t.Fatal("first signal not received")
	}

	mock.set(nil)                         // alert resolves
	time.Sleep(200 * time.Millisecond)    // let adapter remove from seen
	mock.set([]metricsadapter.Alert{      // alert re-triggers
		{ID: "alert-1", Name: "High CPU", Message: "cpu > 90%"},
	})

	select {
	case sig := <-signals:
		assert.Equal(t, "alert-1", sig.Payload["alert_id"])
	case <-ctx.Done():
		t.Fatal("second signal not received after re-arm")
	}
}

func TestMetricsAdapter_NewMetricsAdapter_UnknownProvider(t *testing.T) {
	sig := config.SignalConfig{
		Metrics: config.MetricsSignalConfig{Provider: "splunk"},
	}
	_, err := metricsadapter.NewMetricsAdapter(sig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "splunk")
}

func TestMetricsAdapter_NewMetricsAdapter_PrometheusEmptyEndpoint(t *testing.T) {
	sig := config.SignalConfig{
		Metrics: config.MetricsSignalConfig{Provider: "prometheus", Endpoint: ""},
	}
	_, err := metricsadapter.NewMetricsAdapter(sig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/metrics/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement metrics.go**

Create `internal/adapter/metrics/metrics.go`:

```go
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
// Returns an error if the provider is unknown or required config is missing.
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

	// Collect new signals without holding the lock during channel send.
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
	// Re-arm resolved alerts.
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
```

- [ ] **Step 4: Run all metrics tests**

```bash
go test ./internal/adapter/metrics/... -v
```

Expected: all 6 tests pass (`TestMetricsAdapter_Name`, `TestMetricsAdapter_NewAlertEmitsSignal`, `TestMetricsAdapter_NoDoubleFire`, `TestMetricsAdapter_RearmOnResolve`, `TestMetricsAdapter_NewMetricsAdapter_UnknownProvider`, `TestMetricsAdapter_NewMetricsAdapter_PrometheusEmptyEndpoint`).

Note: `datadog.go` and `prometheus.go` don't exist yet — the package won't compile because `metrics.go` references `NewDatadogProviderWithBaseURL` and `NewPrometheusProvider`. Create compilable stubs first:

Create `internal/adapter/metrics/datadog.go`:

```go
package metrics

import (
	"context"
	"net/http"
)

const datadogBaseURL = "https://api.datadoghq.com"

type DatadogProvider struct{}

func NewDatadogProvider(apiKey, appKey string, tags, alertNames []string, client *http.Client) *DatadogProvider {
	return NewDatadogProviderWithBaseURL(apiKey, appKey, tags, alertNames, client, datadogBaseURL)
}

func NewDatadogProviderWithBaseURL(apiKey, appKey string, tags, alertNames []string, client *http.Client, baseURL string) *DatadogProvider {
	return &DatadogProvider{}
}

func (p *DatadogProvider) FiringAlerts(_ context.Context) ([]Alert, error) { return nil, nil }
```

Create `internal/adapter/metrics/prometheus.go`:

```go
package metrics

import (
	"context"
	"net/http"
)

type PrometheusProvider struct{}

func NewPrometheusProvider(endpoint string, alertNames []string, client *http.Client) *PrometheusProvider {
	return &PrometheusProvider{}
}

func (p *PrometheusProvider) FiringAlerts(_ context.Context) ([]Alert, error) { return nil, nil }
```

Then run the tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/metrics/metrics.go internal/adapter/metrics/metrics_test.go \
        internal/adapter/metrics/datadog.go internal/adapter/metrics/prometheus.go
git commit -m "feat: MetricsAdapter with provider interface and edge-triggered deduplication"
```

---

### Task 4: DatadogProvider

**Files:**
- Modify: `internal/adapter/metrics/datadog.go` (replace stub)
- Create: `internal/adapter/metrics/datadog_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/metrics/datadog_test.go`:

```go
package metrics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metricsadapter "github.com/sausheong/sidecar/internal/adapter/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatadogProvider_FiringAlerts_AlertState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-api-key", r.Header.Get("DD-API-KEY"))
		assert.Equal(t, "test-app-key", r.Header.Get("DD-APPLICATION-KEY"))
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": int64(1001), "name": "High Error Rate", "message": "errors > 5%",
				"overall_state": "Alert", "tags": []string{"env:prod"}},
		})
	}))
	defer server.Close()

	p := metricsadapter.NewDatadogProviderWithBaseURL(
		"test-api-key", "test-app-key", nil, nil, server.Client(), server.URL)
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, "1001", alerts[0].ID)
	assert.Equal(t, "High Error Rate", alerts[0].Name)
	assert.Equal(t, "errors > 5%", alerts[0].Message)
}

func TestDatadogProvider_FiringAlerts_OKState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": int64(1001), "name": "High Error Rate", "message": "msg",
				"overall_state": "OK", "tags": []string{}},
		})
	}))
	defer server.Close()

	p := metricsadapter.NewDatadogProviderWithBaseURL(
		"key", "appkey", nil, nil, server.Client(), server.URL)
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, alerts)
}

func TestDatadogProvider_FiringAlerts_AlertNameFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": int64(1001), "name": "High Error Rate", "message": "msg1",
				"overall_state": "Alert", "tags": []string{}},
			{"id": int64(1002), "name": "High Latency", "message": "msg2",
				"overall_state": "Alert", "tags": []string{}},
		})
	}))
	defer server.Close()

	p := metricsadapter.NewDatadogProviderWithBaseURL(
		"key", "appkey", nil, []string{"High Error Rate"}, server.Client(), server.URL)
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, "High Error Rate", alerts[0].Name)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/metrics/... -v -run TestDatadog
```

Expected: FAIL — stub `FiringAlerts` always returns nil, so `TestDatadogProvider_FiringAlerts_AlertState` gets 0 alerts instead of 1.

- [ ] **Step 3: Implement datadog.go**

Replace `internal/adapter/metrics/datadog.go` with:

```go
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const datadogBaseURL = "https://api.datadoghq.com"

// DatadogProvider fetches firing monitors from the Datadog Monitors API.
type DatadogProvider struct {
	apiKey     string
	appKey     string
	tags       []string
	alertNames []string
	baseURL    string
	client     *http.Client
}

// NewDatadogProvider creates a DatadogProvider using the production Datadog API.
func NewDatadogProvider(apiKey, appKey string, tags, alertNames []string, client *http.Client) *DatadogProvider {
	return NewDatadogProviderWithBaseURL(apiKey, appKey, tags, alertNames, client, datadogBaseURL)
}

// NewDatadogProviderWithBaseURL creates a DatadogProvider with a custom base URL (used in tests).
func NewDatadogProviderWithBaseURL(apiKey, appKey string, tags, alertNames []string, client *http.Client, baseURL string) *DatadogProvider {
	return &DatadogProvider{
		apiKey:     apiKey,
		appKey:     appKey,
		tags:       tags,
		alertNames: alertNames,
		baseURL:    baseURL,
		client:     client,
	}
}

type datadogMonitor struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Message      string   `json:"message"`
	OverallState string   `json:"overall_state"`
	Tags         []string `json:"tags"`
}

// FiringAlerts returns monitors in "Alert" or "Warn" state.
func (p *DatadogProvider) FiringAlerts(ctx context.Context) ([]Alert, error) {
	url := p.baseURL + "/api/v1/monitor"
	if len(p.tags) > 0 {
		url += "?monitor_tags=" + strings.Join(p.tags, ",")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("DD-API-KEY", p.apiKey)
	req.Header.Set("DD-APPLICATION-KEY", p.appKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("datadog api status %d", resp.StatusCode)
	}

	var monitors []datadogMonitor
	if err := json.NewDecoder(resp.Body).Decode(&monitors); err != nil {
		return nil, err
	}

	allowList := make(map[string]bool, len(p.alertNames))
	for _, n := range p.alertNames {
		allowList[n] = true
	}

	var alerts []Alert
	for _, m := range monitors {
		if m.OverallState != "Alert" && m.OverallState != "Warn" {
			continue
		}
		if len(allowList) > 0 && !allowList[m.Name] {
			continue
		}
		msg := m.Message
		if len([]rune(msg)) > 500 {
			msg = string([]rune(msg)[:500])
		}
		alerts = append(alerts, Alert{
			ID:      fmt.Sprintf("%d", m.ID),
			Name:    m.Name,
			Message: msg,
			Labels:  map[string]string{"tags": strings.Join(m.Tags, ",")},
		})
	}
	return alerts, nil
}
```

- [ ] **Step 4: Run all metrics tests**

```bash
go test ./internal/adapter/metrics/... -v
```

Expected: all tests pass (5 adapter + 3 datadog).

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/metrics/datadog.go internal/adapter/metrics/datadog_test.go
git commit -m "feat: DatadogProvider — polls Monitors API for Alert/Warn state monitors"
```

---

### Task 5: PrometheusProvider

**Files:**
- Modify: `internal/adapter/metrics/prometheus.go` (replace stub)
- Create: `internal/adapter/metrics/prometheus_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/metrics/prometheus_test.go`:

```go
package metrics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metricsadapter "github.com/sausheong/sidecar/internal/adapter/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

package metrics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metricsadapter "github.com/sausheong/sidecar/internal/adapter/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makePromServer(t *testing.T, rules []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"groups": []map[string]any{
					{"rules": rules},
				},
			},
		})
	}))
}

func TestPrometheusProvider_FiringAlerts_Firing(t *testing.T) {
	server := makePromServer(t, []map[string]any{
		{"type": "alerting", "name": "HighMemory", "query": "mem > 90",
			"state": "firing", "labels": map[string]string{"severity": "critical"}},
	})
	defer server.Close()

	p := metricsadapter.NewPrometheusProvider(server.URL, nil, server.Client())
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, "HighMemory", alerts[0].Name)
	assert.Equal(t, "mem > 90", alerts[0].Message)
	assert.Equal(t, map[string]string{"severity": "critical"}, alerts[0].Labels)
	assert.NotEmpty(t, alerts[0].ID)
}

func TestPrometheusProvider_FiringAlerts_Inactive(t *testing.T) {
	server := makePromServer(t, []map[string]any{
		{"type": "alerting", "name": "HighMemory", "query": "mem > 90",
			"state": "inactive", "labels": map[string]string{}},
	})
	defer server.Close()

	p := metricsadapter.NewPrometheusProvider(server.URL, nil, server.Client())
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, alerts)
}

func TestPrometheusProvider_AlertID_Stable(t *testing.T) {
	server := makePromServer(t, []map[string]any{
		{"type": "alerting", "name": "HighMemory", "query": "q",
			"state": "firing", "labels": map[string]string{"env": "prod", "job": "api"}},
	})
	defer server.Close()

	p := metricsadapter.NewPrometheusProvider(server.URL, nil, server.Client())

	alerts1, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts1, 1)

	alerts2, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts2, 1)

	assert.Equal(t, alerts1[0].ID, alerts2[0].ID, "alert ID must be stable across polls")
	assert.NotEmpty(t, alerts1[0].ID)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/metrics/... -v -run TestPrometheus
```

Expected: FAIL — stub `FiringAlerts` always returns nil, so `TestPrometheusProvider_FiringAlerts_Firing` gets 0 alerts instead of 1.

- [ ] **Step 3: Implement prometheus.go**

Replace `internal/adapter/metrics/prometheus.go` with:

```go
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// PrometheusProvider fetches firing alerting rules from the Prometheus Rules API.
type PrometheusProvider struct {
	endpoint   string
	alertNames []string
	client     *http.Client
}

// NewPrometheusProvider creates a PrometheusProvider. endpoint is the Prometheus base URL.
func NewPrometheusProvider(endpoint string, alertNames []string, client *http.Client) *PrometheusProvider {
	return &PrometheusProvider{
		endpoint:   strings.TrimRight(endpoint, "/"),
		alertNames: alertNames,
		client:     client,
	}
}

type promRulesResponse struct {
	Status string `json:"status"`
	Data   struct {
		Groups []struct {
			Rules []promRule `json:"rules"`
		} `json:"groups"`
	} `json:"data"`
}

type promRule struct {
	Type   string            `json:"type"`
	Name   string            `json:"name"`
	Query  string            `json:"query"`
	State  string            `json:"state"`
	Labels map[string]string `json:"labels"`
}

// FiringAlerts returns alerting rules currently in "firing" state.
func (p *PrometheusProvider) FiringAlerts(ctx context.Context) ([]Alert, error) {
	url := p.endpoint + "/api/v1/rules?type=alert"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus api status %d", resp.StatusCode)
	}

	var pr promRulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}

	allowList := make(map[string]bool, len(p.alertNames))
	for _, n := range p.alertNames {
		allowList[n] = true
	}

	var alerts []Alert
	for _, group := range pr.Data.Groups {
		for _, rule := range group.Rules {
			if rule.Type != "alerting" || rule.State != "firing" {
				continue
			}
			if len(allowList) > 0 && !allowList[rule.Name] {
				continue
			}
			alerts = append(alerts, Alert{
				ID:      prometheusAlertID(rule.Name, rule.Labels),
				Name:    rule.Name,
				Message: rule.Query,
				Labels:  rule.Labels,
			})
		}
	}
	return alerts, nil
}

// prometheusAlertID produces a stable identifier from alert name and sorted label pairs.
func prometheusAlertID(name string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return name + "|" + strings.Join(parts, ",")
}
```

- [ ] **Step 4: Run all metrics tests**

```bash
go test ./internal/adapter/metrics/... -v
```

Expected: all 11 tests pass (5 adapter + 3 datadog + 3 prometheus).

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/metrics/prometheus.go internal/adapter/metrics/prometheus_test.go
git commit -m "feat: PrometheusProvider — polls Rules API for firing alerting rules"
```

---

### Task 6: Triage Integration

**Files:**
- Modify: `internal/triage/triage.go`
- Modify: `internal/triage/triage_test.go`

- [ ] **Step 1: Write the failing tests**

Read `internal/triage/triage_test.go`. Append:

```go
func TestBuildTriageMessage_MetricAlert(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalMetricAlert,
		Source: "metrics",
		Payload: map[string]any{
			"alert_id":   "1001",
			"alert_name": "High Error Rate",
			"message":    "errors > 5%",
			"provider":   "datadog",
		},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "High Error Rate")
	assert.Contains(t, msg, "errors > 5%")
	assert.Contains(t, msg, "datadog")
}

func TestResolveAutonomy_MetricFix(t *testing.T) {
	cfg := &config.Config{
		Autonomy: config.AutonomyPolicy{
			MetricFixes: "suggest-only",
		},
	}
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("metric_fix", cfg))
}

func TestResolveAutonomy_MetricFix_Default(t *testing.T) {
	cfg := &config.Config{}
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("metric_fix", cfg))
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/triage/... -v -run "TestBuildTriageMessage_MetricAlert|TestResolveAutonomy_MetricFix"
```

Expected: FAIL.

- [ ] **Step 3: Update triage.go**

Read `internal/triage/triage.go`.

**a) Update `TriageResult.ChangeType` comment** (line ~22):

```go
ChangeType string // "test_fix" | "bug_fix" | "dependency_update" | "refactor" | "log_fix" | "metric_fix" | "unknown"
```

**b) Update `triageSystemPrompt`** — add `"metric_fix"` to valid change_type values:

```go
Valid change_type values: "test_fix", "bug_fix", "dependency_update", "refactor", "log_fix", "metric_fix", "unknown"
```

**c) Add `SignalMetricAlert` case to `BuildTriageMessage`** (before `default`):

```go
case adapter.SignalMetricAlert:
	name, _     := sig.Payload["alert_name"].(string)
	message, _  := sig.Payload["message"].(string)
	provider, _ := sig.Payload["provider"].(string)
	return fmt.Sprintf("Metrics alert fired in %s:\nAlert: %s\nDetails: %s\n\nShould this be investigated and fixed automatically?",
		provider, name, message)
```

**d) Add `"metric_fix"` case to `ResolveAutonomy`** (after `"log_fix"` case):

```go
case "metric_fix":
	level = cfg.Autonomy.MetricFixes
```

- [ ] **Step 4: Run all triage tests**

```bash
go test ./internal/triage/... -v
```

Expected: all tests pass (existing + 3 new).

- [ ] **Step 5: Commit**

```bash
git add internal/triage/triage.go internal/triage/triage_test.go
git commit -m "feat: triage handles metric_fix change type and SignalMetricAlert"
```

---

### Task 7: Loop Integration

**Files:**
- Modify: `internal/loop/loop.go`
- Modify: `internal/loop/loop_test.go`

- [ ] **Step 1: Write the failing test**

Read `internal/loop/loop_test.go`. Append:

```go
func TestBuildSystemPrompt_MetricAlert(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalMetricAlert,
		Source: "metrics",
		Payload: map[string]any{
			"alert_id":   "1001",
			"alert_name": "High Error Rate",
			"message":    "http_errors > 5%",
			"provider":   "datadog",
		},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "High Error Rate")
	assert.Contains(t, prompt, "http_errors > 5%")
	assert.Contains(t, prompt, "datadog")
	assert.Contains(t, prompt, "engineering agent")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/loop/... -v -run TestBuildSystemPrompt_MetricAlert
```

Expected: FAIL — falls to `default` case, prompt does not contain "High Error Rate".

- [ ] **Step 3: Update loop.go**

Read `internal/loop/loop.go`.

**a) Add `SignalMetricAlert` case to `BuildSystemPrompt`** (before `default`):

```go
case adapter.SignalMetricAlert:
	name, _     := sig.Payload["alert_name"].(string)
	message, _  := sig.Payload["message"].(string)
	provider, _ := sig.Payload["provider"].(string)
	return fmt.Sprintf(`%s

A metrics alert has fired in the %s monitoring system.

Alert:   %s
Details: %s

Investigate the root cause in the codebase. Check recent changes, identify the code
path responsible, and apply a fix. Run tests to verify your change.`, base, provider, name, message)
```

**b) Add `SignalMetricAlert` case to `userMessage`** (before `default`):

```go
case adapter.SignalMetricAlert:
	name, _     := sig.Payload["alert_name"].(string)
	provider, _ := sig.Payload["provider"].(string)
	return fmt.Sprintf("Metrics alert %q fired in %s. Investigate and fix.", name, provider)
```

**c) Add `SignalMetricAlert` case to `summarize`** (before `default`):

```go
case adapter.SignalMetricAlert:
	name, _ := sig.Payload["alert_name"].(string)
	return fmt.Sprintf("fix metric alert: %s", name)
```

- [ ] **Step 4: Run all loop tests**

```bash
go test ./internal/loop/... -v
```

Expected: all tests pass (existing + 1 new).

- [ ] **Step 5: Commit**

```bash
git add internal/loop/loop.go internal/loop/loop_test.go
git commit -m "feat: loop handles SignalMetricAlert in BuildSystemPrompt, userMessage, summarize"
```

---

### Task 8: CLI Wiring and Final Verification

**Files:**
- Modify: `internal/cli/attach.go`

- [ ] **Step 1: Wire the metrics adapter**

Read `internal/cli/attach.go`. Add the import:

```go
metricsadapter "github.com/sausheong/sidecar/internal/adapter/metrics"
```

Add `case "metrics":` to `buildAdapters` switch (after `case "logs":`):

```go
case "metrics":
	if a, err := metricsadapter.NewMetricsAdapter(sig); err == nil {
		adapters = append(adapters, a)
	} else {
		log.Printf("metrics adapter config error: %v", err)
	}
```

- [ ] **Step 2: Build and verify**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 4: Build binary and verify help**

```bash
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar --help
```

Expected: help output shows `attach`, `task`, `status`, `ask` commands.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/attach.go
git commit -m "feat: wire metrics adapter into sidecar attach"
```

---

## Verification

After all tasks complete:

```bash
go test ./... 2>&1 | grep -v "^?"
```

Expected: all packages pass.

To exercise Datadog end-to-end (requires real credentials):

```yaml
# sidecar.yaml
signals:
  - adapter: metrics
    poll_interval: "60s"
    token: $DATADOG_API_KEY
    metrics:
      provider: datadog
      tags:
        - "env:production"
autonomy:
  metric_fixes: suggest-only
```

```bash
export SIDECAR_DB_URL="postgres://localhost/sidecar"
export ANTHROPIC_API_KEY="..."
export DATADOG_API_KEY="..."
export DATADOG_APP_KEY="..."
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar attach .
```

Expected: sidecar polls Datadog every 60s and emits a suggestion when a monitor enters Alert/Warn state.
