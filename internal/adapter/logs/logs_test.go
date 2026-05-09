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
