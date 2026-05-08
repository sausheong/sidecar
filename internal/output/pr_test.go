package output_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initRepoWithRemote(t *testing.T) (localPath, remotePath string) {
	t.Helper()
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")

	local := t.TempDir()
	runGit(t, local, "clone", remote, ".")
	runGit(t, local, "config", "user.email", "test@test.com")
	runGit(t, local, "config", "user.name", "Test")
	runGit(t, local, "commit", "--allow-empty", "-m", "initial")
	runGit(t, local, "push", "-u", "origin", "master")
	return local, remote
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestPRCreator_FallbackAPI(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/org/repo/pulls", r.URL.Path)
		require.Equal(t, "Bearer testtoken", r.Header.Get("Authorization"))
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://github.com/org/repo/pull/42",
		})
	}))
	defer server.Close()

	local, _ := initRepoWithRemote(t)

	require.NoError(t, os.WriteFile(filepath.Join(local, "fix.go"), []byte("package main"), 0644))
	out := output.New(local)
	branch, err := out.CommitBranch("task-pr-test", "sidecar: test fix")
	require.NoError(t, err)
	require.NotEmpty(t, branch)

	pc := output.NewPRCreatorWithBaseURL(local, "org/repo", "testtoken", server.URL)
	url, err := pc.Create(branch, "sidecar: test fix", "## Sidecar automated fix\n\nTest PR body.")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/org/repo/pull/42", url)
	assert.Equal(t, "sidecar: test fix", receivedBody["title"])
}

func TestPRCreator_DefaultBranch_FallsBackToMain(t *testing.T) {
	local, _ := initRepoWithRemote(t)
	pc := output.NewPRCreatorWithBaseURL(local, "org/repo", "token", "http://localhost:1")
	branch := pc.DefaultBranch()
	assert.NotEmpty(t, branch)
}
