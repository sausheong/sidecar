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
