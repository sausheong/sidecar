package metrics_test

import (
	"context"
	"errors"
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

	mock.set(nil)                      // alert resolves
	time.Sleep(200 * time.Millisecond) // let adapter remove from seen
	mock.set([]metricsadapter.Alert{   // alert re-triggers
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

func TestMetricsAdapter_ProviderErrorDoesNotResetSeen(t *testing.T) {
	mock := &mockProvider{alerts: []metricsadapter.Alert{
		{ID: "alert-1", Name: "High CPU", Message: "cpu > 90%"},
	}}
	a := makeAdapter(mock)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	// First trigger: alert fires and is added to seen.
	select {
	case <-signals:
	case <-ctx.Done():
		t.Fatal("first signal not received")
	}

	// Inject errors — seen map must NOT be cleared.
	mock.mu.Lock()
	mock.err = errors.New("transient timeout")
	mock.mu.Unlock()
	time.Sleep(200 * time.Millisecond)

	// Clear the error, keep same alert firing.
	mock.mu.Lock()
	mock.err = nil
	mock.mu.Unlock()
	time.Sleep(200 * time.Millisecond)

	// Alert should NOT re-fire (still in seen).
	assert.Equal(t, 0, len(signals), "alert must not re-fire after transient error clears")
}
