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
