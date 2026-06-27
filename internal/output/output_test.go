package output_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCommitInPlace_CommitsOnCurrentBranch(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)

	// Clean tree → no commit.
	did, err := o.CommitInPlace("sidecar: noop")
	require.NoError(t, err)
	assert.False(t, did)

	// Dirty tree → commits on the current branch (no new branch created).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("patched"), 0644))
	did, err = o.CommitInPlace("sidecar: applied fix")
	require.NoError(t, err)
	assert.True(t, did)

	out, err := exec.Command("git", "-C", dir, "log", "--oneline", "-1").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "sidecar: applied fix")
}

func TestCommitBranch_Idempotent(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("patched"), 0644))
	branch1, err := o.CommitBranch("task-idem", "sidecar: first")
	require.NoError(t, err)
	assert.Equal(t, "sidecar/task-idem", branch1)

	// Second call with same taskID and new changes should not panic or error
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fix2.txt"), []byte("more"), 0644))
	branch2, err := o.CommitBranch("task-idem", "sidecar: second")
	require.NoError(t, err)
	assert.Equal(t, "sidecar/task-idem", branch2)
}

// headSHA returns the current HEAD commit SHA of dir.
func headSHA(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func TestCommitInPlaceFrom_DirtyCommits(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)
	base := headSHA(t, dir)

	// Dirty working tree → commits and reports a change.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("patched"), 0644))
	changed, err := o.CommitInPlaceFrom(base, "sidecar: applied fix")
	require.NoError(t, err)
	assert.True(t, changed)

	// A new commit exists on top of base.
	out, err := exec.Command("git", "-C", dir, "log", "--oneline", "-1").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "sidecar: applied fix")
	assert.NotEqual(t, base, headSHA(t, dir))
}

func TestCommitInPlaceFrom_CleanButAhead(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)
	base := headSHA(t, dir)

	// Simulate the agent self-committing: clean working tree, but HEAD is
	// ahead of base.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("patched"), 0644))
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "agent self-commit")
	agentHead := headSHA(t, dir)

	changed, err := o.CommitInPlaceFrom(base, "sidecar: applied fix")
	require.NoError(t, err)
	assert.True(t, changed)

	// No NEW commit was created — HEAD is unchanged from the agent's commit.
	assert.Equal(t, agentHead, headSHA(t, dir))
}

func TestCommitInPlaceFrom_CleanAndNotAhead(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)
	base := headSHA(t, dir)

	// Clean tree, no commits ahead of base → no change.
	changed, err := o.CommitInPlaceFrom(base, "sidecar: noop")
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, base, headSHA(t, dir))
}
