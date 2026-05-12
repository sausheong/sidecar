package uptime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	uptimeadapter "github.com/sausheong/sidecar/internal/adapter/uptime"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sigConfig(endpoints []config.UptimeEndpoint) config.SignalConfig {
	return config.SignalConfig{
		PollInterval: "50ms",
		Uptime:       config.UptimeSignalConfig{Endpoints: endpoints},
	}
}

func TestUptimeAdapter_Name(t *testing.T) {
	a := uptimeadapter.New(config.SignalConfig{})
	assert.Equal(t, "uptime", a.Name())
}

func TestUptimeAdapter_HealthyEndpoint_NoSignal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := uptimeadapter.New(sigConfig([]config.UptimeEndpoint{
		{URL: srv.URL, ExpectStatus: 200},
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	<-ctx.Done()
	assert.Equal(t, 0, len(signals), "healthy endpoint must not emit a signal")
}

func TestUptimeAdapter_WrongStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	a := uptimeadapter.New(sigConfig([]config.UptimeEndpoint{
		{URL: srv.URL, ExpectStatus: 200},
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalUptimeFailure, sig.Type)
		assert.Equal(t, "wrong_status", sig.Payload["failure_type"])
		assert.Equal(t, 503, sig.Payload["got_status"])
		assert.Equal(t, 200, sig.Payload["expected_status"])
	case <-ctx.Done():
		t.Fatal("no signal received for wrong status")
	}
}

func TestUptimeAdapter_Unreachable(t *testing.T) {
	a := uptimeadapter.New(sigConfig([]config.UptimeEndpoint{
		{URL: "http://127.0.0.1:19999", Timeout: "200ms"},
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalUptimeFailure, sig.Type)
		assert.Equal(t, "unreachable", sig.Payload["failure_type"])
	case <-ctx.Done():
		t.Fatal("no signal received for unreachable endpoint")
	}
}

func TestUptimeAdapter_SlowResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := uptimeadapter.New(sigConfig([]config.UptimeEndpoint{
		{URL: srv.URL, ExpectStatus: 200, ExpectMaxMs: 50},
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalUptimeFailure, sig.Type)
		assert.Equal(t, "slow_response", sig.Payload["failure_type"])
		assert.Equal(t, 50, sig.Payload["threshold_ms"])
	case <-ctx.Done():
		t.Fatal("no signal received for slow response")
	}
}

func TestUptimeAdapter_DefaultStatus200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated) // 201 != 200
	}))
	defer srv.Close()

	// ExpectStatus omitted — should default to 200
	a := uptimeadapter.New(sigConfig([]config.UptimeEndpoint{
		{URL: srv.URL},
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalUptimeFailure, sig.Type)
		assert.Equal(t, "wrong_status", sig.Payload["failure_type"])
		assert.Equal(t, 200, sig.Payload["expected_status"])
	case <-ctx.Done():
		t.Fatal("no signal: default status should be 200")
	}
}

func TestUptimeAdapter_NoEndpoints(t *testing.T) {
	a := uptimeadapter.New(config.SignalConfig{})
	ctx := context.Background()
	signals := make(chan adapter.Signal, 1)
	require.NoError(t, a.Start(ctx, signals))
	assert.NoError(t, a.Stop())
	assert.Equal(t, 0, len(signals))
}

func TestFormatPayload_Unreachable(t *testing.T) {
	p := map[string]any{"url": "http://example.com", "failure_type": "unreachable", "error": "connection refused"}
	assert.Contains(t, uptimeadapter.FormatPayload(p), "unreachable")
}

func TestFormatPayload_WrongStatus(t *testing.T) {
	p := map[string]any{"url": "http://example.com", "failure_type": "wrong_status", "got_status": 503, "expected_status": 200}
	assert.Contains(t, uptimeadapter.FormatPayload(p), "503")
}

func TestFormatPayload_SlowResponse(t *testing.T) {
	p := map[string]any{"url": "http://example.com", "failure_type": "slow_response", "elapsed_ms": int64(800), "threshold_ms": 500}
	assert.Contains(t, uptimeadapter.FormatPayload(p), "800ms")
}
