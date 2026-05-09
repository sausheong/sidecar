# Sidecar Phase 5 — Logs Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `logs` adapter that watches log files and launched processes for anomalies, emitting `SignalLogAnomaly` into the existing triage → improve loop.

**Architecture:** A new `LogsAdapter` in `internal/adapter/logs/` implements the existing `Adapter` interface using file polling (seek-based, no new deps) and process launching (`exec.Command` + pipe). Keyword matching and rate-based detection both use resolution-based re-arming (quiet period after last match). New `log_fix` change type wired through triage and autonomy config.

**Tech Stack:** Go 1.25+, existing Adapter interface (`Name/Start/Stop`), existing cobra CLI pattern, existing `config.SignalConfig.ParsedPollInterval()`.

---

## File Map

```
internal/
  adapter/
    adapter.go              MODIFY — add SignalLogAnomaly constant
    logs/
      logs.go               CREATE — LogsAdapter, detector logic, file tailing, process launching
      logs_test.go          CREATE — adapter tests via temp files
  config/
    config.go               MODIFY — LogsSignalConfig types + LogFixes in AutonomyPolicy
    logs_config_test.go     CREATE — unmarshal tests for new config types
  loop/
    loop.go                 MODIFY — SignalLogAnomaly case in BuildSystemPrompt, userMessage, summarize
    loop_test.go            MODIFY — TestBuildSystemPrompt_LogAnomaly
  triage/
    triage.go               MODIFY — "log_fix" in triageSystemPrompt, BuildTriageMessage, ResolveAutonomy
    triage_test.go          MODIFY — TestBuildTriageMessage_LogAnomaly, TestResolveAutonomy_LogFix
  cli/
    attach.go               MODIFY — wire logs adapter in buildAdapters switch
```

---

### Task 1: Config Types

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/logs_config_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/config/logs_config_test.go`:

```go
package config_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLogsSignalConfig_Unmarshal(t *testing.T) {
	const data = `
adapter: logs
poll_interval: "2s"
logs:
  files:
    - path: "logs/app.log"
  processes:
    - command: "make serve"
  patterns:
    - match: "ERROR"
      quiet_period: "5m"
  rate:
    window: "30s"
    threshold: 10
    quiet_period: "2m"
`
	var sig config.SignalConfig
	require.NoError(t, yaml.Unmarshal([]byte(data), &sig))
	assert.Equal(t, "logs", sig.Adapter)
	assert.Equal(t, "logs/app.log", sig.Logs.Files[0].Path)
	assert.Equal(t, "make serve", sig.Logs.Processes[0].Command)
	assert.Equal(t, "ERROR", sig.Logs.Patterns[0].Match)
	assert.Equal(t, "5m", sig.Logs.Patterns[0].QuietPeriod)
	assert.Equal(t, "30s", sig.Logs.Rate.Window)
	assert.Equal(t, 10, sig.Logs.Rate.Threshold)
	assert.Equal(t, "2m", sig.Logs.Rate.QuietPeriod)
}

func TestAutonomyPolicy_LogFixes(t *testing.T) {
	const data = `
autonomy:
  log_fixes: suggest-only
`
	var cfg config.Config
	require.NoError(t, yaml.Unmarshal([]byte(data), &cfg))
	assert.Equal(t, "suggest-only", cfg.Autonomy.LogFixes)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/config/... -v -run "TestLogsSignalConfig|TestAutonomyPolicy_LogFixes"
```

Expected: FAIL — `config.SignalConfig` has no field `Logs`, `config.Config.Autonomy` has no field `LogFixes`.

- [ ] **Step 3: Add types to config.go**

Read `internal/config/config.go`. Add these new types after the `EmbeddingConfig` struct (around line 22):

```go
type LogsSignalConfig struct {
	Files     []LogFile     `yaml:"files"`
	Processes []LogProcess  `yaml:"processes"`
	Patterns  []LogPattern  `yaml:"patterns"`
	Rate      LogRateConfig `yaml:"rate"`
}

type LogFile    struct{ Path    string `yaml:"path"` }
type LogProcess struct{ Command string `yaml:"command"` }

type LogPattern struct {
	Match       string `yaml:"match"`
	QuietPeriod string `yaml:"quiet_period"`
}

type LogRateConfig struct {
	Window      string `yaml:"window"`
	Threshold   int    `yaml:"threshold"`
	QuietPeriod string `yaml:"quiet_period"`
}
```

Add `Logs LogsSignalConfig` field to `SignalConfig`:

```go
type SignalConfig struct {
	Adapter      string           `yaml:"adapter"`
	Watch        []string         `yaml:"watch"`
	Cron         string           `yaml:"cron"`
	Repo         string           `yaml:"repo"`
	Token        string           `yaml:"token"`
	PollInterval string           `yaml:"poll_interval"`
	Logs         LogsSignalConfig `yaml:"logs"`
}
```

Add `LogFixes` field to `AutonomyPolicy`:

```go
type AutonomyPolicy struct {
	DependencyUpdates string `yaml:"dependency_updates"`
	TestFixes         string `yaml:"test_fixes"`
	BugFixes          string `yaml:"bug_fixes"`
	Refactoring       string `yaml:"refactoring"`
	SchemaChanges     string `yaml:"schema_changes"`
	LogFixes          string `yaml:"log_fixes"`
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/config/... -v
```

Expected: all tests pass (2 new + any existing).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/logs_config_test.go
git commit -m "feat: add LogsSignalConfig types and LogFixes autonomy field"
```

---

### Task 2: Signal Type Constant

**Files:**
- Modify: `internal/adapter/adapter.go`

- [ ] **Step 1: Add the constant**

Read `internal/adapter/adapter.go`. Add `SignalLogAnomaly` to the existing const block:

```go
const (
	SignalGitCommit    SignalType = "git.commit"
	SignalScheduleTick SignalType = "schedule.tick"
	SignalOnDemand     SignalType = "ondemand.task"
	SignalCIFailure    SignalType = "ci.failure"
	SignalLogAnomaly   SignalType = "log.anomaly"
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
git commit -m "feat: add SignalLogAnomaly signal type constant"
```

---

### Task 3: LogsAdapter

**Files:**
- Create: `internal/adapter/logs/logs.go`
- Create: `internal/adapter/logs/logs_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/logs/logs_test.go`:

```go
package logs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	logsadapter "github.com/sausheong/sidecar/internal/adapter/logs"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogsAdapter_Name(t *testing.T) {
	a := logsadapter.New(config.SignalConfig{})
	assert.Equal(t, "logs", a.Name())
}

func TestLogsAdapter_Stop(t *testing.T) {
	a := logsadapter.New(config.SignalConfig{})
	ctx := context.Background()
	signals := make(chan adapter.Signal, 1)
	require.NoError(t, a.Start(ctx, signals))
	assert.NoError(t, a.Stop())
}

func TestLogsAdapter_FileKeywordMatch(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.log")
	require.NoError(t, err)
	_, _ = f.WriteString("startup line\n")
	f.Sync()

	sig := config.SignalConfig{
		PollInterval: "50ms",
		Logs: config.LogsSignalConfig{
			Files: []config.LogFile{{Path: f.Name()}},
			Patterns: []config.LogPattern{
				{Match: "ERROR", QuietPeriod: "1m"},
			},
		},
	}

	a := logsadapter.New(sig)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(100 * time.Millisecond) // let adapter seek to EOF

	_, _ = f.WriteString("2026-05-09 ERROR: nil pointer\n")
	f.Sync()

	select {
	case got := <-signals:
		assert.Equal(t, adapter.SignalLogAnomaly, got.Type)
		assert.Equal(t, "logs", got.Source)
		assert.Equal(t, "ERROR", got.Payload["pattern"])
		assert.Equal(t, f.Name(), got.Payload["source"])
		assert.Equal(t, 1, got.Payload["count"])
	case <-ctx.Done():
		t.Fatal("no signal received within timeout")
	}
}

func TestLogsAdapter_FileKeywordNoDoubleFire(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.log")
	require.NoError(t, err)
	_, _ = f.WriteString("startup\n")
	f.Sync()

	sig := config.SignalConfig{
		PollInterval: "50ms",
		Logs: config.LogsSignalConfig{
			Files: []config.LogFile{{Path: f.Name()}},
			Patterns: []config.LogPattern{
				{Match: "ERROR", QuietPeriod: "10m"},
			},
		},
	}

	a := logsadapter.New(sig)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(100 * time.Millisecond)

	_, _ = f.WriteString("ERROR: first\nERROR: second\n")
	f.Sync()

	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, 1, len(signals), "second ERROR must not fire while pattern is disarmed")
}

func TestLogsAdapter_FileRateThreshold(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.log")
	require.NoError(t, err)
	_, _ = f.WriteString("startup\n")
	f.Sync()

	sig := config.SignalConfig{
		PollInterval: "50ms",
		Logs: config.LogsSignalConfig{
			Files: []config.LogFile{{Path: f.Name()}},
			Patterns: []config.LogPattern{
				{Match: "WARN", QuietPeriod: "10m"},
			},
			Rate: config.LogRateConfig{
				Window:      "5s",
				Threshold:   3,
				QuietPeriod: "1m",
			},
		},
	}

	a := logsadapter.New(sig)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(100 * time.Millisecond)

	_, _ = f.WriteString("WARN: one\nWARN: two\nWARN: three\n")
	f.Sync()

	time.Sleep(300 * time.Millisecond)

	var rateSignal *adapter.Signal
	for len(signals) > 0 {
		s := <-signals
		sp := s
		if sp.Payload["pattern"] == "rate" {
			rateSignal = &sp
		}
	}
	require.NotNil(t, rateSignal, "rate signal should have fired after threshold reached")
	assert.Equal(t, adapter.SignalLogAnomaly, rateSignal.Type)
	assert.Equal(t, "rate", rateSignal.Payload["pattern"])
	assert.Equal(t, 3, rateSignal.Payload["count"])
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/logs/... -v
```

Expected: FAIL — package `logs` does not exist yet.

- [ ] **Step 3: Implement logs.go**

Create `internal/adapter/logs/logs.go`:

```go
package logs

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
)

const defaultPoll = 2 * time.Second

type patternState struct {
	re          *regexp.Regexp
	raw         string
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
	matches     []time.Time
}

// LogsAdapter watches log files and launched processes for keyword and rate anomalies.
type LogsAdapter struct {
	cfg      config.LogsSignalConfig
	poll     time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	mu       sync.Mutex
	patterns []patternState
	rate     rateState
}

// New creates a LogsAdapter from a signal config.
// poll_interval defaults to 2s when the field is empty.
func New(sig config.SignalConfig) *LogsAdapter {
	poll := sig.ParsedPollInterval()
	if sig.PollInterval == "" {
		poll = defaultPoll
	}
	return &LogsAdapter{
		cfg:    sig.Logs,
		poll:   poll,
		stopCh: make(chan struct{}),
	}
}

func (a *LogsAdapter) Name() string { return "logs" }

// Start compiles patterns, initialises rate state, and launches goroutines for
// each file, process, and the re-arm ticker.
func (a *LogsAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	for _, p := range a.cfg.Patterns {
		re, err := regexp.Compile(p.Match)
		if err != nil {
			re = regexp.MustCompile(regexp.QuoteMeta(p.Match))
		}
		qp, _ := time.ParseDuration(p.QuietPeriod)
		if qp <= 0 {
			qp = 5 * time.Minute
		}
		a.patterns = append(a.patterns, patternState{
			re:          re,
			raw:         p.Match,
			quietPeriod: qp,
			armed:       true,
		})
	}

	if a.cfg.Rate.Threshold > 0 {
		rw, _ := time.ParseDuration(a.cfg.Rate.Window)
		if rw <= 0 {
			rw = 30 * time.Second
		}
		rqp, _ := time.ParseDuration(a.cfg.Rate.QuietPeriod)
		if rqp <= 0 {
			rqp = 2 * time.Minute
		}
		a.rate = rateState{
			window:      rw,
			threshold:   a.cfg.Rate.Threshold,
			quietPeriod: rqp,
			armed:       true,
		}
	}

	for _, f := range a.cfg.Files {
		f := f
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.tailFile(ctx, f.Path, out)
		}()
	}

	for _, p := range a.cfg.Processes {
		p := p
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.runProcess(ctx, p.Command, out)
		}()
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.rearmLoop()
	}()

	return nil
}

// Stop closes the stop channel and waits for all goroutines to exit.
func (a *LogsAdapter) Stop() error {
	a.stopOnce.Do(func() { close(a.stopCh) })
	a.wg.Wait()
	return nil
}

func (a *LogsAdapter) tailFile(ctx context.Context, path string, out chan<- adapter.Signal) {
	f, err := os.Open(path)
	if err != nil {
		slog.Warn("logs adapter: cannot open file", "path", path, "err", err)
		return
	}
	defer f.Close()

	offset, _ := f.Seek(0, io.SeekEnd)

	ticker := time.NewTicker(a.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			info, err := f.Stat()
			if err != nil {
				continue
			}
			if info.Size() < offset {
				// Log rotation: restart from beginning.
				offset, _ = f.Seek(0, io.SeekStart)
				continue
			}
			if info.Size() == offset {
				continue
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			if err != nil || len(data) == 0 {
				continue
			}
			// Only process complete lines (up to last newline) to avoid partial reads.
			lastNL := bytes.LastIndexByte(data, '\n')
			if lastNL < 0 {
				continue
			}
			offset += int64(lastNL) + 1
			for _, raw := range bytes.Split(data[:lastNL], []byte("\n")) {
				if line := string(raw); line != "" {
					a.processLine(line, path, out)
				}
			}
		}
	}
}

func (a *LogsAdapter) runProcess(ctx context.Context, command string, out chan<- adapter.Signal) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		slog.Warn("logs adapter: cannot start process", "command", command, "err", err)
		pw.Close()
		pr.Close()
		return
	}

	go func() {
		_ = cmd.Wait()
		pw.Close()
	}()

	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		select {
		case <-a.stopCh:
			_ = cmd.Process.Kill()
			pr.Close()
			return
		default:
			a.processLine(scanner.Text(), command, out)
		}
	}
	pr.Close()
}

// processLine checks a single log line against armed keyword patterns and the
// rate window. Emits SignalLogAnomaly when a pattern or rate threshold fires.
func (a *LogsAdapter) processLine(line, source string, out chan<- adapter.Signal) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()

	// Keyword matching: each armed pattern fires at most once per quiet period.
	for i := range a.patterns {
		p := &a.patterns[i]
		if !p.armed || !p.re.MatchString(line) {
			continue
		}
		p.armed = false
		p.lastMatch = now
		select {
		case <-a.stopCh:
			return
		case out <- adapter.Signal{
			Type:   adapter.SignalLogAnomaly,
			Source: "logs",
			Payload: map[string]any{
				"pattern": p.raw,
				"line":    line,
				"source":  source,
				"count":   1,
			},
		}:
		}
	}

	// Rate tracking: counts all pattern matches in the sliding window.
	if a.rate.threshold == 0 || !a.rate.armed {
		return
	}
	for _, p := range a.patterns {
		if !p.re.MatchString(line) {
			continue
		}
		cutoff := now.Add(-a.rate.window)
		j := 0
		for _, t := range a.rate.matches {
			if t.After(cutoff) {
				a.rate.matches[j] = t
				j++
			}
		}
		a.rate.matches = append(a.rate.matches[:j], now)

		if len(a.rate.matches) >= a.rate.threshold {
			a.rate.armed = false
			a.rate.lastFire = now
			count := len(a.rate.matches)
			a.rate.matches = nil
			select {
			case <-a.stopCh:
				return
			case out <- adapter.Signal{
				Type:   adapter.SignalLogAnomaly,
				Source: "logs",
				Payload: map[string]any{
					"pattern": "rate",
					"line":    line,
					"source":  source,
					"count":   count,
				},
			}:
			}
		}
		break
	}
}

// rearmLoop re-arms keyword patterns and rate state once their quiet period elapses.
func (a *LogsAdapter) rearmLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			a.mu.Lock()
			for i := range a.patterns {
				p := &a.patterns[i]
				if !p.armed && !p.lastMatch.IsZero() && now.Sub(p.lastMatch) >= p.quietPeriod {
					p.armed = true
				}
			}
			if !a.rate.armed && !a.rate.lastFire.IsZero() && now.Sub(a.rate.lastFire) >= a.rate.quietPeriod {
				a.rate.armed = true
			}
			a.mu.Unlock()
		}
	}
}
```

- [ ] **Step 4: Run all logs tests**

```bash
go test ./internal/adapter/logs/... -v
```

Expected: all 5 tests pass (`TestLogsAdapter_Name`, `TestLogsAdapter_Stop`, `TestLogsAdapter_FileKeywordMatch`, `TestLogsAdapter_FileKeywordNoDoubleFire`, `TestLogsAdapter_FileRateThreshold`).

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/logs/logs.go internal/adapter/logs/logs_test.go
git commit -m "feat: LogsAdapter — file tailing and process watching with keyword and rate detection"
```

---

### Task 4: Triage Integration

**Files:**
- Modify: `internal/triage/triage.go`
- Modify: `internal/triage/triage_test.go`

- [ ] **Step 1: Write the failing tests**

Read `internal/triage/triage_test.go`. Append these two tests:

```go
func TestBuildTriageMessage_LogAnomaly(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalLogAnomaly,
		Source: "logs",
		Payload: map[string]any{
			"pattern": "ERROR",
			"line":    "2026-05-09 ERROR: nil pointer dereference",
			"source":  "logs/app.log",
			"count":   1,
		},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "ERROR")
	assert.Contains(t, msg, "logs/app.log")
	assert.Contains(t, msg, "nil pointer dereference")
}

func TestResolveAutonomy_LogFix(t *testing.T) {
	cfg := &config.Config{
		Autonomy: config.AutonomyPolicy{
			LogFixes: "suggest-only",
		},
	}
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("log_fix", cfg))
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/triage/... -v -run "TestBuildTriageMessage_LogAnomaly|TestResolveAutonomy_LogFix"
```

Expected: FAIL — `BuildTriageMessage` has no `SignalLogAnomaly` case; `ResolveAutonomy` has no `"log_fix"` case.

- [ ] **Step 3: Update triage.go**

Read `internal/triage/triage.go`.

**a) Update `triageSystemPrompt`** — add `"log_fix"` to the valid change_type list (line 39):

```go
const triageSystemPrompt = `You are a triage agent for an autonomous software engineering system.
Analyze the incoming signal and decide:
1. Whether it warrants autonomous action (should_act: true/false)
2. If so, what type of change is needed

Valid change_type values: "test_fix", "bug_fix", "dependency_update", "refactor", "log_fix", "unknown"

Do NOT act (should_act: false) for:
- CI failures caused by infrastructure issues (network timeouts, disk full, runner unavailable)
- Documentation-only commits that cannot cause test failures
- Signals with insufficient information to determine a fix

Respond with ONLY valid JSON, no prose:
{"should_act": true, "change_type": "test_fix", "reason": "one sentence"}`
```

**b) Add `SignalLogAnomaly` case to `BuildTriageMessage`** (after the `default` case, line 107):

```go
case adapter.SignalLogAnomaly:
	pattern, _ := sig.Payload["pattern"].(string)
	source, _ := sig.Payload["source"].(string)
	line, _ := sig.Payload["line"].(string)
	return fmt.Sprintf("Log anomaly detected:\nPattern: %s\nSource: %s\nSample: %s\n\nShould this be investigated and fixed automatically?",
		pattern, source, line)
```

**c) Add `"log_fix"` case to `ResolveAutonomy`** (after the `"refactor"` case, line 142):

```go
case "log_fix":
	level = cfg.Autonomy.LogFixes
```

- [ ] **Step 4: Run all triage tests**

```bash
go test ./internal/triage/... -v
```

Expected: all triage tests pass (7 existing + 2 new).

- [ ] **Step 5: Commit**

```bash
git add internal/triage/triage.go internal/triage/triage_test.go
git commit -m "feat: triage handles log_fix change type and SignalLogAnomaly"
```

---

### Task 5: Loop Integration

**Files:**
- Modify: `internal/loop/loop.go`
- Modify: `internal/loop/loop_test.go`

- [ ] **Step 1: Write the failing test**

Read `internal/loop/loop_test.go`. Append:

```go
func TestBuildSystemPrompt_LogAnomaly(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalLogAnomaly,
		Source: "logs",
		Payload: map[string]any{
			"pattern": "ERROR",
			"line":    "ERROR: nil pointer dereference at main.go:42",
			"source":  "logs/app.log",
			"count":   1,
		},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "ERROR")
	assert.Contains(t, prompt, "logs/app.log")
	assert.Contains(t, prompt, "nil pointer dereference")
	assert.Contains(t, prompt, "engineering agent")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/loop/... -v -run TestBuildSystemPrompt_LogAnomaly
```

Expected: FAIL — `BuildSystemPrompt` has no `SignalLogAnomaly` case; falls to `default` which uses `sig.Payload["description"]` (empty), so prompt does not contain "ERROR".

- [ ] **Step 3: Update loop.go**

Read `internal/loop/loop.go`.

**a) Add `SignalLogAnomaly` case to `BuildSystemPrompt`** (before the `default` case, around line 323):

```go
case adapter.SignalLogAnomaly:
	pattern, _ := sig.Payload["pattern"].(string)
	source, _ := sig.Payload["source"].(string)
	line, _ := sig.Payload["line"].(string)
	return fmt.Sprintf(`%s

A log anomaly was detected in the running application.

Pattern: %s
Source:  %s
Sample:  %s

Investigate the root cause. Check relevant code paths, reproduce the issue if possible,
and apply a fix. Run tests to verify your change.`, base, pattern, source, line)
```

**b) Add `SignalLogAnomaly` case to `userMessage`** (before the `default` case, around line 346):

```go
case adapter.SignalLogAnomaly:
	pattern, _ := sig.Payload["pattern"].(string)
	source, _ := sig.Payload["source"].(string)
	return fmt.Sprintf("Log anomaly detected: pattern %q in %s. Investigate and fix.", pattern, source)
```

**c) Add `SignalLogAnomaly` case to `summarize`** (before the `default` case, around line 373):

```go
case adapter.SignalLogAnomaly:
	pattern, _ := sig.Payload["pattern"].(string)
	source, _ := sig.Payload["source"].(string)
	return fmt.Sprintf("fix log anomaly: %s in %s", pattern, source)
```

- [ ] **Step 4: Run all loop tests**

```bash
go test ./internal/loop/... -v
```

Expected: all loop tests pass (8 existing + 1 new = 9 total).

- [ ] **Step 5: Commit**

```bash
git add internal/loop/loop.go internal/loop/loop_test.go
git commit -m "feat: loop handles SignalLogAnomaly in BuildSystemPrompt, userMessage, summarize"
```

---

### Task 6: CLI Wiring and Final Verification

**Files:**
- Modify: `internal/cli/attach.go`

- [ ] **Step 1: Wire the logs adapter in attach.go**

Read `internal/cli/attach.go`. 

Add the import for the logs adapter (with alias to avoid collision) in the import block alongside the other adapter imports:

```go
import (
	// ... existing imports ...
	logsadapter "github.com/sausheong/sidecar/internal/adapter/logs"
)
```

Add a `case "logs":` to the `buildAdapters` switch (after the `"github-ci"` case, around line 119):

```go
case "logs":
	adapters = append(adapters, logsadapter.New(sig))
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

- [ ] **Step 4: Build the binary and verify help**

```bash
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar --help
```

Expected output includes: `attach`, `task`, `status`, `ask` commands.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/attach.go
git commit -m "feat: wire logs adapter into sidecar attach"
```

---

## Verification

After all tasks complete:

```bash
go test ./... 2>&1 | grep -v "^?"
```

Expected: all packages pass with no failures.

To exercise the logs adapter end-to-end (requires a workspace with `sidecar.yaml`):

```yaml
# sidecar.yaml excerpt
signals:
  - adapter: logs
    poll_interval: "2s"
    logs:
      files:
        - path: "logs/app.log"
      patterns:
        - match: "ERROR"
          quiet_period: "5m"
autonomy:
  log_fixes: suggest-only
```

```bash
export SIDECAR_DB_URL="postgres://localhost/sidecar"
export ANTHROPIC_API_KEY="..."

mkdir -p logs && echo "startup" > logs/app.log
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar attach .  # in a separate terminal

# Trigger a log anomaly:
echo "2026-05-09 ERROR: nil pointer dereference at main.go:42" >> logs/app.log
```

Expected: sidecar detects the anomaly, runs triage, and records a suggestion (with `log_fixes: suggest-only`).
