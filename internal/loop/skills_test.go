package loop_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSkillsProvider_NilWhenAbsent(t *testing.T) {
	repo := t.TempDir()
	cfg := &config.Config{}
	assert.Nil(t, loop.BuildSkillsProvider(repo, cfg))
}

func TestBuildSkillsProvider_NonNilWhenPresent(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".sidecar", "skills"), 0o755))
	cfg := &config.Config{}
	assert.NotNil(t, loop.BuildSkillsProvider(repo, cfg))
}

func TestBuildSkillsProvider_RespectsConfiguredDir(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "ops", "skills"), 0o755))
	cfg := &config.Config{Skills: config.SkillsConfig{Dir: "ops/skills"}}
	assert.NotNil(t, loop.BuildSkillsProvider(repo, cfg))
}
