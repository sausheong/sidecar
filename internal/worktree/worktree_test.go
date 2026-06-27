package worktree_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/worktree"
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

func TestCreate_IsolatedDirAndBranch(t *testing.T) {
	repo := initRepo(t)

	wt, cleanup, err := worktree.Create(repo, "task-123")
	require.NoError(t, err)
	require.NotNil(t, wt)

	assert.Equal(t, "sidecar/task-123", wt.Branch)
	assert.NotEqual(t, repo, wt.Path)

	// Base is captured as the 40-hex SHA the worktree was created from.
	assert.Regexp(t, "^[0-9a-f]{40}$", wt.Base)

	// The worktree dir exists and is a working tree.
	info, err := os.Stat(wt.Path)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// A file written in the worktree does not appear in the main repo.
	require.NoError(t, os.WriteFile(filepath.Join(wt.Path, "only-here.txt"), []byte("x"), 0644))
	_, err = os.Stat(filepath.Join(repo, "only-here.txt"))
	assert.True(t, os.IsNotExist(err))

	// Cleanup removes the worktree dir but the branch survives in the main repo.
	require.NoError(t, cleanup())
	_, err = os.Stat(wt.Path)
	assert.True(t, os.IsNotExist(err))

	out, err := exec.Command("git", "-C", repo, "branch", "--list", "sidecar/task-123").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "sidecar/task-123")
}
