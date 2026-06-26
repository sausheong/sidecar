package triage_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/triage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTriageResponse_Valid(t *testing.T) {
	raw := `{"should_act": true, "change_type": "test_fix", "reason": "3 assertions failing"}`
	result, err := triage.ParseTriageResponse(raw)
	require.NoError(t, err)
	assert.True(t, result.ShouldAct)
	assert.Equal(t, "test_fix", result.ChangeType)
	assert.Equal(t, "3 assertions failing", result.Reason)
}

func TestParseTriageResponse_ShouldNotAct(t *testing.T) {
	raw := `{"should_act": false, "change_type": "unknown", "reason": "docs-only change"}`
	result, err := triage.ParseTriageResponse(raw)
	require.NoError(t, err)
	assert.False(t, result.ShouldAct)
}

func TestParseTriageResponse_Invalid(t *testing.T) {
	_, err := triage.ParseTriageResponse("not json at all")
	assert.Error(t, err)
}

func TestResolveAutonomy(t *testing.T) {
	cfg := &config.Config{
		Autonomy: config.AutonomyPolicy{
			TestFixes:         "auto-commit",
			BugFixes:          "pull-request",
			DependencyUpdates: "auto-commit",
			Refactoring:       "suggest-only",
		},
	}

	assert.Equal(t, "auto-commit", triage.ResolveAutonomy("test_fix", cfg))
	assert.Equal(t, "pull-request", triage.ResolveAutonomy("bug_fix", cfg))
	assert.Equal(t, "auto-commit", triage.ResolveAutonomy("dependency_update", cfg))
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("refactor", cfg))
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("unknown", cfg))
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("schema_change", cfg)) // unmapped → safe default
}

func TestBuildTriageMessage_CIFailure(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalCIFailure,
		Source: "github-ci",
		Payload: map[string]any{
			"workflow_name": "CI",
			"conclusion":    "failure",
			"head_sha":      "abc123",
			"html_url":      "https://github.com/org/repo/actions/runs/1",
			"repo":          "org/repo",
			"is_flake":      false,
		},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "CI")
	assert.Contains(t, msg, "abc123")
	assert.Contains(t, msg, "failure")
}

func TestBuildTriageMessage_OnDemand(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalOnDemand,
		Payload: map[string]any{"description": "fix the auth bug"},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "fix the auth bug")
}

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

func TestBuildTriageMessage_MetricAlert(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalMetricAlert,
		Source: "metrics",
		Payload: map[string]any{
			"alert_id":   "1001",
			"alert_name": "High Error Rate",
			"message":    "errors > 5%",
			"provider":   "datadog",
		},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "High Error Rate")
	assert.Contains(t, msg, "errors > 5%")
	assert.Contains(t, msg, "datadog")
}

func TestResolveAutonomy_MetricFix(t *testing.T) {
	cfg := &config.Config{
		Autonomy: config.AutonomyPolicy{
			MetricFixes: "suggest-only",
		},
	}
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("metric_fix", cfg))
}

func TestResolveAutonomy_MetricFix_Default(t *testing.T) {
	cfg := &config.Config{}
	assert.Equal(t, "suggest-only", triage.ResolveAutonomy("metric_fix", cfg))
}

func TestBuildTriageMessage_CIFailure_CircleCI(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalCIFailure,
		Source: "circleci",
		Payload: map[string]any{
			"workflow_name": "build-and-test",
			"conclusion":    "failed",
			"head_sha":      "abc123",
			"html_url":      "https://app.circleci.com/pipelines/gh/org/repo/42",
			"repo":          "gh/org/repo",
			"is_flake":      false,
		},
	}
	msg := triage.BuildTriageMessage(sig)
	assert.Contains(t, msg, "circleci")
	assert.Contains(t, msg, "build-and-test")
	assert.Contains(t, msg, "failed")
}

func TestTriageSystemPrompt_EnumeratesAllChangeTypes(t *testing.T) {
	msg := triage.BuildTriageMessage(adapter.Signal{
		Type:    adapter.SignalUptimeFailure,
		Payload: map[string]any{"url": "https://x", "failure_type": "wrong_status"},
	})
	// BuildTriageMessage is the user turn; the enum lives in the system prompt,
	// exposed for testing via SystemPrompt().
	_ = msg
	sp := triage.SystemPrompt()
	for _, ct := range []string{"test_fix", "bug_fix", "dependency_update", "refactor", "log_fix", "metric_fix", "uptime_fix"} {
		assert.Contains(t, sp, ct, "change_type %q must be enumerated in the triage system prompt", ct)
	}
}
