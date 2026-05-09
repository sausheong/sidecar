# Sidecar Phase 5 — Logs Adapter Design Spec

**Date:** 2026-05-09
**Status:** Approved
**Builds on:** Phase 1–4 (core runtime, reactive CI, persistent memory, proactive runtime — complete)

---

## 1. Overview

Phase 5 adds a `logs` adapter that watches log files and launched processes for anomalies, emitting a new `SignalLogAnomaly` signal that feeds into the existing triage → improve loop.

Two detection modes run concurrently:

1. **Keyword matching** — checks each line against configured patterns (substrings or regex). Fires immediately on first match; disarms until the pattern is absent for `quiet_period`, then re-arms automatically.
2. **Rate-based** — counts all pattern matches in a sliding time window; fires when `threshold` is exceeded within `window`; re-arms after `quiet_period` of sub-threshold activity.

Two source types:

- **Files** — one goroutine per file, seeks to EOF on start, polls for new lines on `poll_interval`.
- **Processes** — adapter launches the command via `sh -c`, reads stdout+stderr pipes line by line until the process exits or the adapter is stopped.

No new dependencies. Same polling philosophy as the GitHub CI adapter.

---

## 2. New Components

```
internal/
  adapter/
    adapter.go        MODIFY — add SignalLogAnomaly constant
    logs/
      logs.go         CREATE — LogsAdapter, detector logic
      logs_test.go    CREATE — unit tests
  config/
    config.go         MODIFY — LogsSignalConfig types, LogFixes in AutonomyPolicy
  loop/
    loop.go           MODIFY — SignalLogAnomaly case in BuildSystemPrompt
  triage/
    triage.go         MODIFY — "log_fix" change type in BuildTriageMessage + ResolveAutonomy
  cli/
    attach.go         MODIFY — wire logs adapter when adapter="logs"
```

---

## 3. Config Schema

### New types in `internal/config/config.go`

```go
type LogsSignalConfig struct {
    Files     []LogFile     `yaml:"files"`
    Processes []LogProcess  `yaml:"processes"`
    Patterns  []LogPattern  `yaml:"patterns"`
    Rate      LogRateConfig `yaml:"rate"`
}

type LogFile    struct { Path    string `yaml:"path"` }
type LogProcess struct { Command string `yaml:"command"` }

type LogPattern struct {
    Match       string `yaml:"match"`        // substring or regex
    QuietPeriod string `yaml:"quiet_period"` // e.g. "5m"
}

type LogRateConfig struct {
    Window      string `yaml:"window"`      // e.g. "30s"
    Threshold   int    `yaml:"threshold"`   // N matches in Window triggers
    QuietPeriod string `yaml:"quiet_period"`
}
```

`LogsSignalConfig` is added as a new field on `SignalConfig`:

```go
type SignalConfig struct {
    // ... existing fields unchanged ...
    Logs LogsSignalConfig `yaml:"logs"`
}
```

`AutonomyPolicy` gains one new field:

```go
type AutonomyPolicy struct {
    // ... existing fields unchanged ...
    LogFixes string `yaml:"log_fixes"`
}
```

### Example `sidecar.yaml`

```yaml
signals:
  - adapter: logs
    poll_interval: "2s"
    logs:
      files:
        - path: "logs/app.log"
        - path: "tmp/server.log"
      processes:
        - command: "make serve"
      patterns:
        - match: "ERROR"
          quiet_period: "5m"
        - match: "PANIC"
          quiet_period: "10m"
        - match: "fatal"
          quiet_period: "5m"
      rate:
        window: "30s"
        threshold: 10
        quiet_period: "2m"

autonomy:
  log_fixes: suggest-only
```

`poll_interval` reuses the existing `SignalConfig.PollInterval` field. Default for the logs adapter is `"2s"` (vs `"60s"` for CI); the adapter applies `ParsedPollInterval()` and uses 2s if the field is empty.

---

## 4. Adapter Implementation (`internal/adapter/logs/logs.go`)

### Structs

```go
type LogsAdapter struct {
    cfg     config.LogsSignalConfig
    poll    time.Duration
    stop    chan struct{}
    once    sync.Once
    wg      sync.WaitGroup
    mu      sync.Mutex      // guards patterns and rate
    patterns []patternState
    rate     rateState
}

type patternState struct {
    re          *regexp.Regexp
    raw         string        // original match string (for payload)
    quietPeriod time.Duration
    armed       bool
    lastMatch   time.Time
}

type rateState struct {
    window      time.Duration
    threshold   int
    quietPeriod time.Duration
    armed       bool
    lastFire    time.Time
    matches     []time.Time   // timestamps within current window
}
```

### `Start(ctx context.Context, out chan<- Signal) error`

Initialises `patternState` entries (compiling regex), sets all patterns and rate armed=true.

Launches goroutines (tracked via `wg`):
- One per `LogFile`: opens file, seeks to EOF, reads new lines every `poll_interval`; if the file shrinks between polls (detected by comparing current size to last read offset), re-seeks to offset 0 to handle log rotation
- One per `LogProcess`: `exec.CommandContext(ctx, "sh", "-c", cmd)` with stdout+stderr piped via `io.MultiReader`, reads line by line
- One rearm goroutine: ticks every second, re-arms any disarmed pattern/rate state whose `quiet_period` has elapsed since `lastMatch`/`lastFire`

All goroutines call the shared `processLine(line, source string)` method.

### `processLine(line, source string)`

Mutex-guarded. For each armed `patternState`: if `re.MatchString(line)`, emit `SignalLogAnomaly` and disarm (set `lastMatch = now`, `armed = false`).

For rate state: trim `matches` to entries within the current `window`, append `now`. If `len(matches) >= threshold` and rate is armed, emit `SignalLogAnomaly` with `pattern: "rate"` and disarm (set `lastFire = now`, `armed = false`, clear `matches`).

### Signal payload

```go
map[string]any{
    "pattern": "ERROR",           // matched pattern string, or "rate"
    "line":    "<matched line>",  // the triggering log line
    "source":  "logs/app.log",   // file path or process command
    "count":   1,                 // 1 for keyword match; N for rate trigger
}
```

### `Stop() error`

Closes `stop` channel via `sync.Once`. File goroutines select on `stop`; process goroutines are cancelled via context. Calls `wg.Wait()` before returning.

### Default poll interval

`NewLogsAdapter(sig config.SignalConfig) *LogsAdapter` uses `sig.ParsedPollInterval()` with a 2s fallback (overrides the 60s default by checking if the raw `PollInterval` field is empty and substituting `"2s"`).

---

## 5. Signal Type

```go
// internal/adapter/adapter.go
SignalLogAnomaly SignalType = "log.anomaly"
```

---

## 6. Loop Integration (`internal/loop/loop.go`)

New case in `BuildSystemPrompt`:

```go
case adapter.SignalLogAnomaly:
    pattern, _ := sig.Payload["pattern"].(string)
    source, _  := sig.Payload["source"].(string)
    line, _    := sig.Payload["line"].(string)
    return fmt.Sprintf(`%s

A log anomaly was detected in the running application.

Pattern: %s
Source:  %s
Sample:  %s

Investigate the root cause. Check relevant code paths, reproduce the issue if possible,
and apply a fix. Run tests to verify your change.`, base, pattern, source, line)
```

---

## 7. Triage Integration (`internal/triage/triage.go`)

`BuildTriageMessage` adds `"log_fix"` to the valid change type list presented to the Haiku model.

`ResolveAutonomy` maps `"log_fix"` → `cfg.Autonomy.LogFixes`. Fallback when `LogFixes` is empty: `"suggest-only"` (conservative default for runtime anomalies).

---

## 8. CLI Wiring (`internal/cli/attach.go`)

When a signal config has `Adapter == "logs"`, `attach.go` constructs `logs.NewLogsAdapter(sig)` and adds it to the daemon's adapter list. Same pattern as the `git` and `github-ci` adapters.

---

## 9. Testing Strategy

`logs_test.go` tests pure logic with no filesystem or process dependencies:

- `TestProcessLine_KeywordMatch` — armed pattern fires on matching line, disarms
- `TestProcessLine_KeywordMatch_Disarmed` — disarmed pattern does not fire
- `TestProcessLine_RateThreshold` — N matches in window fires rate signal
- `TestProcessLine_RateThreshold_BelowThreshold` — N-1 matches does not fire
- `TestRearm_PatternAfterQuietPeriod` — disarmed pattern re-arms after quiet_period elapsed
- `TestRearm_RateAfterQuietPeriod` — disarmed rate re-arms after quiet_period elapsed
- `TestBuildLogsSystemPrompt` — `BuildSystemPrompt` for `SignalLogAnomaly` contains expected strings

Integration tests (build tag `integration`) use a temp file to exercise real file tailing end-to-end.

---

## 10. Non-Goals for Phase 5

- Metrics adapter (Datadog/Prometheus) — Phase 6
- Additional CI providers (GitLab, CircleCI) — Phase 7
- Structured log parsing (JSON log lines, log levels as fields)
- Log rotation detection (adapter re-opens the file if it shrinks, covering simple rotation)
- Log history backfill (adapter always starts from EOF, not beginning of file)
- Process restart on exit (if a watched process exits, the goroutine ends cleanly; no auto-restart)
