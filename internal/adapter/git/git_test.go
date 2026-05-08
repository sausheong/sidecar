package git_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	gitadapter "github.com/sausheong/sidecar/internal/adapter/git"
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

func addCommit(t *testing.T, dir, msg string) {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", msg).CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestGitAdapter_DetectsNewCommit(t *testing.T) {
	dir := initRepo(t)

	a := gitadapter.New(dir)
	a.PollInterval = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	// Let one poll run (sees no new commits)
	time.Sleep(200 * time.Millisecond)
	assert.Empty(t, signals)

	// Add a commit — adapter should detect it on next poll
	addCommit(t, dir, "test: add something")
	time.Sleep(300 * time.Millisecond)

	require.Len(t, signals, 1)
	sig := <-signals
	assert.Equal(t, adapter.SignalGitCommit, sig.Type)
	assert.Equal(t, "git", sig.Source)
	assert.NotEmpty(t, sig.Payload["hash"])
	assert.Equal(t, dir, sig.Payload["repo"])
}

func TestGitAdapter_NoDuplicates(t *testing.T) {
	dir := initRepo(t)
	addCommit(t, dir, "pre-existing commit")

	a := gitadapter.New(dir)
	a.PollInterval = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	// Wait for two polls — should see no signals (commit was already there)
	time.Sleep(350 * time.Millisecond)
	assert.Empty(t, signals, "pre-existing commit should not trigger a signal")
}
