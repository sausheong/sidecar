package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachCmd_RequiresYAML(t *testing.T) {
	root := cli.RootCmd()
	root.SetArgs([]string{"attach", "/nonexistent/path"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sidecar.yaml")
}

func TestAttachCmd_RequiresDBURL_WhenCIAdapterConfigured(t *testing.T) {
	dir := t.TempDir()
	yaml := `
workspace:
  name: test
signals:
  - adapter: github-ci
    repo: org/repo
    token: $GITHUB_TOKEN
    watch: [failure]
autonomy:
  test_fixes: auto-commit
`
	err := os.WriteFile(filepath.Join(dir, "sidecar.yaml"), []byte(yaml), 0644)
	require.NoError(t, err)

	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"attach", dir})
	err = root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}

func TestAttachCmd_MemoryDisabledWithoutEmbeddingConfig(t *testing.T) {
	dir := t.TempDir()
	yaml := `
workspace:
  name: test
signals:
  - adapter: git
    watch: [push]
autonomy:
  test_fixes: auto-commit
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sidecar.yaml"), []byte(yaml), 0644))

	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"attach", dir})
	err := root.Execute()
	// Should fail on SIDECAR_DB_URL, not on embedding config
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}
