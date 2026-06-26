// Package worktree isolates each code-shipping task in its own git worktree,
// so concurrent agent runs never share a working directory (the paper's
// "Tangled Loop" anti-pattern).
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Worktree is an isolated git working directory on a dedicated branch.
type Worktree struct {
	Path   string
	Branch string
}

// Create adds a git worktree at a fresh temp dir on branch sidecar/<taskID>,
// based on the current HEAD of repoPath. The returned cleanup func removes the
// worktree directory (the branch persists in the shared .git so PR creation
// still works). Callers should defer cleanup().
func Create(repoPath, taskID string) (*Worktree, func() error, error) {
	branch := "sidecar/" + taskID
	dir, err := os.MkdirTemp("", "sidecar-wt-"+taskID+"-")
	if err != nil {
		return nil, nil, fmt.Errorf("creating worktree tmpdir: %w", err)
	}
	// git worktree add refuses a pre-existing non-empty dir; remove the stub.
	if err := os.RemoveAll(dir); err != nil {
		return nil, nil, fmt.Errorf("clearing worktree tmpdir: %w", err)
	}

	out, err := exec.Command("git", "-C", repoPath, "worktree", "add", "-b", branch, dir).CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("git worktree add: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	wt := &Worktree{Path: dir, Branch: branch}
	cleanup := func() error {
		o, e := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", dir).CombinedOutput()
		if e != nil {
			// Best-effort dir removal so we don't leak temp dirs on git error.
			_ = os.RemoveAll(dir)
			return fmt.Errorf("git worktree remove: %w\n%s", e, strings.TrimSpace(string(o)))
		}
		return nil
	}
	return wt, cleanup, nil
}
