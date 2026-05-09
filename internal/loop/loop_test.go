package loop_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/stretchr/testify/assert"
)

func TestBuildSystemPrompt_GitCommit(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalGitCommit,
		Source:  "git",
		Payload: map[string]any{"hash": "abc123", "repo": "/tmp/myrepo"},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "abc123")
	assert.Contains(t, prompt, "engineering agent")
}

func TestBuildSystemPrompt_ScheduleTick(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalScheduleTick,
		Source:  "schedule",
		Payload: map[string]any{},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "proactive")
	assert.Contains(t, prompt, "engineering agent")
}

func TestBuildSystemPrompt_OnDemand(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalOnDemand,
		Source:  "cli",
		Payload: map[string]any{"description": "fix the flaky test in auth_test.go"},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "fix the flaky test")
}

func TestDefaultModels(t *testing.T) {
	cfg := &config.Config{}
	m := loop.ResolveModels(cfg)
	assert.NotEmpty(t, m.Coding)
	assert.NotEmpty(t, m.Triage)
}

func TestBuildSystemPrompt_CIFailure(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalCIFailure,
		Source: "github-ci",
		Payload: map[string]any{
			"workflow_name": "CI",
			"conclusion":    "failure",
			"head_sha":      "abc123",
			"html_url":      "https://github.com/org/repo/actions/runs/1",
		},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "CI")
	assert.Contains(t, prompt, "abc123")
	assert.Contains(t, prompt, "engineering agent")
}

func TestStatusConstants(t *testing.T) {
	assert.Equal(t, "skipped", loop.StatusSkipped)
	assert.Equal(t, "suggested", loop.StatusSuggested)
}

func TestLoop_MemoryNilSafe(t *testing.T) {
	// ResolveModels should work regardless of embedding provider
	cfg := &config.Config{}
	models := loop.ResolveModels(cfg)
	assert.NotEmpty(t, models.Coding)
	assert.NotEmpty(t, models.Triage)
}

func TestBuildSystemPrompt_ScheduleTick_MemoryGuided(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalScheduleTick,
		Source:  "schedule",
		Payload: map[string]any{},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "fragile")
	assert.Contains(t, prompt, "workspace memory")
	assert.Contains(t, prompt, "engineering agent")
}

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

func TestBuildSystemPrompt_MetricAlert(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalMetricAlert,
		Source: "metrics",
		Payload: map[string]any{
			"alert_id":   "1001",
			"alert_name": "High Error Rate",
			"message":    "http_errors > 5%",
			"provider":   "datadog",
		},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "High Error Rate")
	assert.Contains(t, prompt, "http_errors > 5%")
	assert.Contains(t, prompt, "datadog")
	assert.Contains(t, prompt, "engineering agent")
}

func TestBuildSystemPrompt_CIFailure_GitLab(t *testing.T) {
	sig := adapter.Signal{
		Type:   adapter.SignalCIFailure,
		Source: "gitlab-ci",
		Payload: map[string]any{
			"workflow_name": "main",
			"conclusion":    "failed",
			"head_sha":      "abc123",
			"html_url":      "https://gitlab.com/mygroup/myproject/-/pipelines/1",
		},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "gitlab-ci")
	assert.Contains(t, prompt, "main")
	assert.Contains(t, prompt, "abc123")
	assert.Contains(t, prompt, "engineering agent")
}
