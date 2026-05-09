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
	openFile := func() (*os.File, int64) {
		f, err := os.Open(path)
		if err != nil {
			slog.Warn("logs adapter: cannot open file", "path", path, "err", err)
			return nil, 0
		}
		offset, _ := f.Seek(0, io.SeekEnd)
		return f, offset
	}

	f, offset := openFile()
	if f == nil {
		return
	}
	defer func() { f.Close() }()

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
			// Detect rotation: either the file shrank (truncation) or the
			// path now points to a different inode (rename/remove+recreate).
			rotated := info.Size() < offset
			if !rotated {
				if pathInfo, err := os.Stat(path); err == nil {
					rotated = !os.SameFile(info, pathInfo)
				}
			}
			if rotated {
				// Log rotation: close old inode, reopen at path.
				f.Close()
				f, offset = openFile()
				if f == nil {
					return
				}
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

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		pw.Close()
		close(done)
	}()

	// Kill proactively when stop is requested, so Stop() unblocks promptly.
	go func() {
		select {
		case <-a.stopCh:
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()

	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		a.processLine(scanner.Text(), command, out)
	}
	pr.Close()
}

// processLine checks a single log line against armed keyword patterns and the
// rate window. Emits SignalLogAnomaly when a pattern or rate threshold fires.
func (a *LogsAdapter) processLine(line, source string, out chan<- adapter.Signal) {
	a.mu.Lock()
	now := time.Now()
	var toSend []adapter.Signal

	// Keyword matching: each armed pattern fires at most once per quiet period.
	for i := range a.patterns {
		p := &a.patterns[i]
		if !p.armed || !p.re.MatchString(line) {
			continue
		}
		p.armed = false
		p.lastMatch = now
		toSend = append(toSend, adapter.Signal{
			Type:   adapter.SignalLogAnomaly,
			Source: "logs",
			Payload: map[string]any{
				"pattern": p.raw,
				"line":    line,
				"source":  source,
				"count":   1,
			},
		})
	}

	// Rate tracking: counts all pattern matches in the sliding window.
	if a.rate.threshold > 0 && a.rate.armed {
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
				toSend = append(toSend, adapter.Signal{
					Type:   adapter.SignalLogAnomaly,
					Source: "logs",
					Payload: map[string]any{
						"pattern": "rate",
						"line":    line,
						"source":  source,
						"count":   count,
					},
				})
			}
			break
		}
	}
	a.mu.Unlock()

	for _, sig := range toSend {
		select {
		case <-a.stopCh:
			return
		case out <- sig:
		}
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
