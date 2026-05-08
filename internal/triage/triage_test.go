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
