package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	yaml := `
workspace:
  name: my-service
  language: go
signals:
  - adapter: git
    watch: [push, pr]
  - adapter: schedule
    cron: "0 2 * * *"
autonomy:
  dependency_updates: auto-commit
  test_fixes: auto-commit
  bug_fixes: pull-request
  refactoring: suggest-only
  schema_changes: suggest-only
models:
  planning: anthropic/claude-sonnet-4-6
  coding: anthropic/claude-sonnet-4-6
  triage: anthropic/claude-haiku-4-5
scope:
  include: [src/, tests/]
  exclude: [secrets/]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, "my-service", cfg.Workspace.Name)
	assert.Equal(t, "go", cfg.Workspace.Language)
	assert.Len(t, cfg.Signals, 2)
	assert.Equal(t, "git", cfg.Signals[0].Adapter)
	assert.Equal(t, []string{"push", "pr"}, cfg.Signals[0].Watch)
	assert.Equal(t, "schedule", cfg.Signals[1].Adapter)
	assert.Equal(t, "0 2 * * *", cfg.Signals[1].Cron)
	assert.Equal(t, "auto-commit", cfg.Autonomy.DependencyUpdates)
	assert.Equal(t, "pull-request", cfg.Autonomy.BugFixes)
	assert.Equal(t, "anthropic/claude-haiku-4-5", cfg.Models.Triage)
	assert.Equal(t, []string{"src/", "tests/"}, cfg.Scope.Include)
	assert.Equal(t, []string{"secrets/"}, cfg.Scope.Exclude)
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := config.Load("/does/not/exist/sidecar.yaml")
	assert.Error(t, err)
}

func TestLoad_GitHubCI(t *testing.T) {
	yaml := `
signals:
  - adapter: github-ci
    repo: myorg/payment-service
    token: $GITHUB_TOKEN
    poll_interval: 60s
    watch: [failure]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	require.Len(t, cfg.Signals, 1)
	s := cfg.Signals[0]
	assert.Equal(t, "github-ci", s.Adapter)
	assert.Equal(t, "myorg/payment-service", s.Repo)
	assert.Equal(t, "$GITHUB_TOKEN", s.Token)
	assert.Equal(t, "60s", s.PollInterval)
	assert.Equal(t, []string{"failure"}, s.Watch)
}

func TestAutonomyLevel_Valid(t *testing.T) {
	cases := []string{"auto-commit", "pull-request", "suggest-only"}
	for _, c := range cases {
		assert.True(t, config.ValidAutonomyLevel(c), "expected %q to be valid", c)
	}
	assert.False(t, config.ValidAutonomyLevel("invalid"))
}

func TestSignalConfig_ParsedPollInterval(t *testing.T) {
	assert.Equal(t, 60*time.Second, config.SignalConfig{}.ParsedPollInterval())                        // empty → default
	assert.Equal(t, 30*time.Second, config.SignalConfig{PollInterval: "30s"}.ParsedPollInterval())     // valid
	assert.Equal(t, 60*time.Second, config.SignalConfig{PollInterval: "invalid"}.ParsedPollInterval()) // bad → default
	assert.Equal(t, 60*time.Second, config.SignalConfig{PollInterval: "-5s"}.ParsedPollInterval())     // negative → default
}

func TestSignalConfig_ResolveToken(t *testing.T) {
	t.Setenv("MY_TEST_TOKEN", "secret123")
	assert.Equal(t, "secret123", config.SignalConfig{Token: "$MY_TEST_TOKEN"}.ResolveToken()) // env var
	assert.Equal(t, "literal-token", config.SignalConfig{Token: "literal-token"}.ResolveToken()) // literal
	assert.Equal(t, "", config.SignalConfig{Token: ""}.ResolveToken())                           // empty
}

func TestLoad_Embedding(t *testing.T) {
	yaml := `
embedding:
  provider: openai
  model: text-embedding-3-small
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, "openai", cfg.Embedding.Provider)
	assert.Equal(t, "text-embedding-3-small", cfg.Embedding.Model)
}

func TestLoad_Embedding_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.yaml")
	require.NoError(t, os.WriteFile(path, []byte("workspace:\n  name: test\n"), 0644))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, "", cfg.Embedding.Provider)
}
