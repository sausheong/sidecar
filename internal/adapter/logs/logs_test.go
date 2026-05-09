package logs_test

import (
	"context"
	"os"
	"path/filepath"
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

func TestLogsAdapter_ProcessKeywordMatch(t *testing.T) {
	sig := config.SignalConfig{
		PollInterval: "50ms",
		Logs: config.LogsSignalConfig{
			Processes: []config.LogProcess{
				{Command: `echo "ERROR: from process"`},
			},
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

	select {
	case got := <-signals:
		assert.Equal(t, adapter.SignalLogAnomaly, got.Type)
		assert.Equal(t, "logs", got.Source)
		assert.Equal(t, "ERROR", got.Payload["pattern"])
	case <-ctx.Done():
		t.Fatal("no signal received from process within timeout")
	}
}

func TestLogsAdapter_RearmAfterQuietPeriod(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.log")
	require.NoError(t, err)
	_, _ = f.WriteString("startup\n")
	f.Sync()

	sig := config.SignalConfig{
		PollInterval: "50ms",
		Logs: config.LogsSignalConfig{
			Files: []config.LogFile{{Path: f.Name()}},
			Patterns: []config.LogPattern{
				{Match: "ERROR", QuietPeriod: "200ms"}, // short quiet period for testing
			},
		},
	}

	a := logsadapter.New(sig)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(100 * time.Millisecond) // let adapter seek to EOF

	// First match — should fire and disarm
	_, _ = f.WriteString("ERROR: first\n")
	f.Sync()

	select {
	case got := <-signals:
		assert.Equal(t, adapter.SignalLogAnomaly, got.Type)
	case <-ctx.Done():
		t.Fatal("first signal not received")
	}

	// Wait for quiet period (200ms) to elapse and for the rearmLoop ticker (1s) to fire.
	time.Sleep(1500 * time.Millisecond)

	// Second match — should fire again after re-arming
	_, _ = f.WriteString("ERROR: second\n")
	f.Sync()

	select {
	case got := <-signals:
		assert.Equal(t, adapter.SignalLogAnomaly, got.Type)
	case <-ctx.Done():
		t.Fatal("second signal not received after re-arm")
	}
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

func TestLogsAdapter_FileRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	// Create initial log file
	f, err := os.Create(path)
	require.NoError(t, err)
	_, _ = f.WriteString("startup\n")
	f.Sync()

	sig := config.SignalConfig{
		PollInterval: "50ms",
		Logs: config.LogsSignalConfig{
			Files: []config.LogFile{{Path: path}},
			Patterns: []config.LogPattern{
				{Match: "ERROR", QuietPeriod: "1m"},
			},
		},
	}

	a := logsadapter.New(sig)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	time.Sleep(100 * time.Millisecond) // let adapter seek to EOF

	// Simulate log rotation: replace file with a new one at the same path
	f.Close()
	require.NoError(t, os.Remove(path))
	f2, err := os.Create(path)
	require.NoError(t, err)
	defer f2.Close()

	// Give adapter a couple of poll cycles to detect the rotation
	time.Sleep(200 * time.Millisecond)

	// Write a matching line to the new file
	_, _ = f2.WriteString("ERROR: after rotation\n")
	f2.Sync()

	select {
	case got := <-signals:
		assert.Equal(t, adapter.SignalLogAnomaly, got.Type)
		assert.Equal(t, "ERROR", got.Payload["pattern"])
	case <-ctx.Done():
		t.Fatal("no signal received after log rotation")
	}
}
