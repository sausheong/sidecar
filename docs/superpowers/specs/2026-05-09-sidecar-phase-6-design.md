# Sidecar Phase 6 — Metrics Adapter Design Spec

**Date:** 2026-05-09
**Status:** Approved
**Builds on:** Phase 1–5 (core runtime, reactive CI, persistent memory, proactive runtime, logs adapter — complete)

---

## 1. Overview

Phase 6 adds a `metrics` adapter that polls Datadog and Prometheus for firing alerts, emitting `SignalMetricAlert` into the existing triage → improve loop.

**Key principle:** Edge-triggered deduplication. The adapter tracks a `seen map[string]bool` of currently-firing alert IDs (same pattern as the GitHub CI adapter). A new alert fires the signal once; when the alert resolves (absent from the next poll), it is removed from `seen` and will fire again if it re-triggers.

**Pluggable provider model:** `MetricsProvider` interface defined in `internal/adapter/metrics/metrics.go`, with Datadog and Prometheus as two concrete implementations in the same package. The interface is only consumed by the metrics adapter, so it lives co-located rather than in a separate package.

---

## 2. New Components

```
internal/
  adapter/
    adapter.go              MODIFY — add SignalMetricAlert constant
    metrics/
      metrics.go            CREATE — MetricsAdapter, MetricsProvider interface, Alert type, NewMetricsAdapter
      datadog.go            CREATE — DatadogProvider (Monitors API)
      prometheus.go         CREATE — PrometheusProvider (Rules API)
      metrics_test.go       CREATE — adapter tests via mock provider
      datadog_test.go       CREATE — httptest-based unit tests
      prometheus_test.go    CREATE — httptest-based unit tests
  config/
    config.go               MODIFY — MetricsSignalConfig, MetricFixes in AutonomyPolicy
  loop/
    loop.go                 MODIFY — SignalMetricAlert cases in BuildSystemPrompt, userMessage, summarize
  triage/
    triage.go               MODIFY — "metric_fix" change type in triageSystemPrompt, BuildTriageMessage, ResolveAutonomy
  cli/
    attach.go               MODIFY — wire metrics adapter in buildAdapters switch
```

---

## 3. Signal Type

```go
// internal/adapter/adapter.go
SignalMetricAlert SignalType = "metric.alert"
```

---

## 4. Config Schema

### New types in `internal/config/config.go`

```go
type MetricsSignalConfig struct {
    Provider   string   `yaml:"provider"`    // "datadog" | "prometheus"
    Endpoint   string   `yaml:"endpoint"`    // Prometheus base URL, e.g. "http://localhost:9090"
    Tags       []string `yaml:"tags"`        // Datadog: filter monitors by tags
    AlertNames []string `yaml:"alert_names"` // optional allowlist; empty = all alerts
}
```

`MetricsSignalConfig` is added as a new field on `SignalConfig`:

```go
type SignalConfig struct {
    // ... existing fields unchanged ...
    Metrics MetricsSignalConfig `yaml:"metrics"`
}
```

`AutonomyPolicy` gains one new field:

```go
type AutonomyPolicy struct {
    // ... existing fields unchanged ...
    MetricFixes string `yaml:"metric_fixes"`
}
```

`ResolveAutonomy` maps `"metric_fix"` → `cfg.Autonomy.MetricFixes`, defaulting to `"suggest-only"` when the field is empty.

### Authentication

Datadog requires two keys:
- **API key:** read from `sig.ResolveToken()` (existing `token` field in `SignalConfig`, supports `$ENV_VAR` syntax)
- **Application key:** read from `os.Getenv("DATADOG_APP_KEY")` directly in the adapter

Prometheus requires no auth by default (typically internal). The base URL is configured via `metrics.endpoint`.

### Example `sidecar.yaml`

```yaml
signals:
  - adapter: metrics
    poll_interval: "60s"
    token: $DATADOG_API_KEY
    metrics:
      provider: datadog
      tags:
        - "env:production"
      alert_names:
        - "High Error Rate"

  - adapter: metrics
    poll_interval: "30s"
    metrics:
      provider: prometheus
      endpoint: "http://localhost:9090"

autonomy:
  metric_fixes: suggest-only
```

---

## 5. MetricsProvider Interface and Alert Type

```go
// Alert is a single firing alert returned by a MetricsProvider.
type Alert struct {
    ID      string            // stable unique identifier across polls
    Name    string            // human-readable alert name
    Message string            // alert description or condition
    Labels  map[string]string // tags/labels for context
}

// MetricsProvider polls a metrics backend for currently firing alerts.
type MetricsProvider interface {
    FiringAlerts(ctx context.Context) ([]Alert, error)
}
```

---

## 6. MetricsAdapter

```go
type MetricsAdapter struct {
    provider   MetricsProvider
    poll       time.Duration
    providerName string        // "datadog" | "prometheus" (for signal payload)
    seen       map[string]bool // alert IDs currently firing
    seenMu     sync.Mutex
    stopCh     chan struct{}
    stopOnce   sync.Once
    wg         sync.WaitGroup
}
```

### `NewMetricsAdapter(sig config.SignalConfig) (*MetricsAdapter, error)`

Reads `sig.Metrics.Provider` and constructs the appropriate provider:
- `"datadog"` → `NewDatadogProvider(sig.ResolveToken(), os.Getenv("DATADOG_APP_KEY"), sig.Metrics.Tags, sig.Metrics.AlertNames, http.DefaultClient)`
- `"prometheus"` → `NewPrometheusProvider(sig.Metrics.Endpoint, sig.Metrics.AlertNames, http.DefaultClient)` — returns error if `Endpoint` is empty
- Unknown provider → returns error

`Name() string` returns `"metrics"`.

### `Start(ctx context.Context, out chan<- adapter.Signal) error`

Launches one goroutine:

```
for each poll tick:
  alerts, err := provider.FiringAlerts(ctx)
  if err: log warning, continue

  build current = set of alert IDs from alerts
  seenMu.Lock()
    for each alert in alerts:
      if alert.ID not in seen:
        emit SignalMetricAlert
        seen[alert.ID] = true
    for id in seen:
      if id not in current:
        delete seen[id]   // alert resolved — re-armed
  seenMu.Unlock()
```

### Signal payload

```go
map[string]any{
    "alert_id":   alert.ID,
    "alert_name": alert.Name,
    "message":    alert.Message,
    "labels":     alert.Labels,          // map[string]string
    "provider":   "datadog"|"prometheus",
}
```

### `Stop() error`

Closes `stopCh` via `sync.Once`, then calls `wg.Wait()` to ensure the poll goroutine has exited before returning.

---

## 7. Datadog Provider (`datadog.go`)

```go
type DatadogProvider struct {
    apiKey     string
    appKey     string
    tags       []string
    alertNames []string
    baseURL    string
    client     *http.Client
}

func NewDatadogProvider(apiKey, appKey string, tags, alertNames []string, client *http.Client) *DatadogProvider
func NewDatadogProviderWithBaseURL(apiKey, appKey string, tags, alertNames []string, client *http.Client, baseURL string) *DatadogProvider
```

**API:** `GET {baseURL}/api/v1/monitor`

**Query parameters:**
- `monitor_tags`: comma-joined `tags` (if non-empty)

Alert name filtering is always done client-side after fetching.

**Auth headers:**
- `DD-API-KEY: <apiKey>`
- `DD-APPLICATION-KEY: <appKey>`

**Alert condition:** monitor `overall_state` == `"Alert"` or `"Warn"`

**Alert fields:**
- `ID`: `strconv.FormatInt(monitor.ID, 10)`
- `Name`: monitor's `name` field
- `Message`: monitor's `message` field, truncated to 500 characters
- `Labels`: `{"tags": strings.Join(monitor.Tags, ",")}` (Datadog monitors have tag lists, not key-value labels)

**Filtering:** if `alertNames` is non-empty, client-side filter to only include monitors whose name is in the allowlist.

---

## 8. Prometheus Provider (`prometheus.go`)

```go
type PrometheusProvider struct {
    endpoint   string
    alertNames []string
    client     *http.Client
}

func NewPrometheusProvider(endpoint string, alertNames []string, client *http.Client) *PrometheusProvider
```

**API:** `GET {endpoint}/api/v1/rules?type=alert`

**No auth** (Prometheus is typically internal).

**Alert condition:** rule `state` == `"firing"`

**Alert ID:** stable hash of `alertname + sorted label key=value pairs`:
```go
// Produces a deterministic string like: "HighMemoryUsage|env=prod,job=api"
func alertID(name string, labels map[string]string) string
```

**Alert fields:**
- `ID`: `alertID(rule.Name, rule.Labels)`
- `Name`: rule's `name` field
- `Message`: rule's `query` field (the PromQL expression)
- `Labels`: rule's `labels` map

**Filtering:** if `alertNames` is non-empty, client-side filter on rule name.

**API response shape:**
```json
{
  "status": "success",
  "data": {
    "groups": [
      {
        "rules": [
          {
            "type": "alerting",
            "name": "HighMemoryUsage",
            "query": "process_resident_memory_bytes > 1e9",
            "state": "firing",
            "labels": {"severity": "critical"}
          }
        ]
      }
    ]
  }
}
```

---

## 9. Loop Integration

New case in `BuildSystemPrompt`:

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

New case in `userMessage`:

```go
case adapter.SignalMetricAlert:
    name, _     := sig.Payload["alert_name"].(string)
    provider, _ := sig.Payload["provider"].(string)
    return fmt.Sprintf("Metrics alert %q fired in %s. Investigate and fix.", name, provider)
```

New case in `summarize`:

```go
case adapter.SignalMetricAlert:
    name, _ := sig.Payload["alert_name"].(string)
    return fmt.Sprintf("fix metric alert: %s", name)
```

---

## 10. Triage Integration

`triageSystemPrompt` valid change types updated to include `"metric_fix"`.

`BuildTriageMessage` adds:

```go
case adapter.SignalMetricAlert:
    name, _     := sig.Payload["alert_name"].(string)
    message, _  := sig.Payload["message"].(string)
    provider, _ := sig.Payload["provider"].(string)
    return fmt.Sprintf("Metrics alert fired in %s:\nAlert: %s\nDetails: %s\n\nShould this be investigated and fixed automatically?",
        provider, name, message)
```

`ResolveAutonomy` adds:

```go
case "metric_fix":
    level = cfg.Autonomy.MetricFixes
```

`TriageResult.ChangeType` comment updated to include `"metric_fix"`.

---

## 11. CLI Wiring

`buildAdapters` in `attach.go` adds:

```go
case "metrics":
    if a, err := metricsadapter.NewMetricsAdapter(sig); err == nil {
        adapters = append(adapters, a)
    } else {
        log.Printf("metrics adapter config error: %v", err)
    }
```

Error is logged and the adapter is skipped (same pattern as the `schedule` adapter).

---

## 12. Testing Strategy

**`metrics_test.go`** — adapter tests via a mock `MetricsProvider`:
- `TestMetricsAdapter_Name` — returns `"metrics"`
- `TestMetricsAdapter_NewAlertEmitsSignal` — mock returns one alert; assert signal emitted
- `TestMetricsAdapter_NoDoubleFire` — mock returns same alert twice; assert signal emitted once
- `TestMetricsAdapter_RearmOnResolve` — mock returns alert, then empty, then same alert; assert signal emitted twice (once per trigger)

**`datadog_test.go`** — `httptest.NewServer` mocking the Datadog API:
- `TestDatadogProvider_FiringAlerts_AlertState` — returns monitor with `overall_state: "Alert"`; assert one alert returned
- `TestDatadogProvider_FiringAlerts_OKState` — returns monitor with `overall_state: "OK"`; assert no alerts
- `TestDatadogProvider_FiringAlerts_AlertNameFilter` — returns two monitors; `alertNames` filters to one

**`prometheus_test.go`** — `httptest.NewServer` mocking the Prometheus Rules API:
- `TestPrometheusProvider_FiringAlerts_Firing` — returns rule with `state: "firing"`; assert one alert
- `TestPrometheusProvider_FiringAlerts_Inactive` — returns rule with `state: "inactive"`; assert no alerts
- `TestPrometheusProvider_AlertID_Stable` — same alert name + labels always produces same ID

---

## 13. Non-Goals for Phase 6

- Additional CI providers (GitLab, CircleCI) — Phase 7
- Alertmanager integration (Prometheus alerting rules API is sufficient)
- Datadog Events or Logs APIs (monitors only)
- Alert silence/mute detection (firing = act, resolved = re-arm, no other states)
- Webhook-based alert delivery (pull/poll only, no push)
- Metric querying or threshold evaluation (Sidecar reacts to existing alert definitions, does not define them)
