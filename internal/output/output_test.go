package output_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}

func TestCommitBranch_NoChanges(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)

	branch, err := o.CommitBranch("task-abc", "sidecar: test task")
	require.NoError(t, err)
	assert.Equal(t, output.BranchNoChanges, branch)
}

func TestCommitBranch_WithChanges(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("patched"), 0644))

	branch, err := o.CommitBranch("task-xyz", "sidecar: applied fix")
	require.NoError(t, err)
	assert.Equal(t, "sidecar/task-xyz", branch)

	out, err := exec.Command("git", "-C", dir, "log", "--oneline", "sidecar/task-xyz").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "sidecar: applied fix")
}
