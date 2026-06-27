package output

import (
	"fmt"
	"os/exec"
	"strings"
)

const BranchNoChanges = ""

type Output struct {
	repoPath string
}

func New(repoPath string) *Output {
	return &Output{repoPath: repoPath}
}

// CommitBranch creates a sidecar/<taskID> branch, stages all changes, and
// commits them. Returns BranchNoChanges if the working tree is clean.
func (o *Output) CommitBranch(taskID, message string) (string, error) {
	if clean, err := o.isClean(); err != nil {
		return "", err
	} else if clean {
		return BranchNoChanges, nil
	}

	branch := "sidecar/" + taskID
	cmds := [][]string{
		{"git", "-C", o.repoPath, "checkout", "-B", branch},
		{"git", "-C", o.repoPath, "add", "-A"},
		{"git", "-C", o.repoPath, "commit", "-m", message},
		{"git", "-C", o.repoPath, "checkout", "-"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %s: %w\n%s", args[3], err, out)
		}
	}
	return branch, nil
}

// CommitInPlace stages and commits all changes on the CURRENT branch (used
// when the caller is already inside an isolated worktree on its own branch).
// Returns true if a commit was made, false if the working tree was clean.
func (o *Output) CommitInPlace(message string) (bool, error) {
	if clean, err := o.isClean(); err != nil {
		return false, err
	} else if clean {
		return false, nil
	}
	cmds := [][]string{
		{"git", "-C", o.repoPath, "add", "-A"},
		{"git", "-C", o.repoPath, "commit", "-m", message},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return false, fmt.Errorf("git %s: %w\n%s", args[3], err, out)
		}
	}
	return true, nil
}

// CommitInPlaceFrom commits/detects changes on the CURRENT branch relative to
// baseRef, so an agent self-commit is not mistaken for "nothing to do".
//
// Returns true if the branch has ANY change vs baseRef:
//   - dirty working tree → git add -A + git commit, return true
//   - clean tree but commits ahead of baseRef (agent already committed) →
//     return true WITHOUT a new commit
//   - clean tree and no commits ahead → return false
//
// If baseRef is "" it behaves like CommitInPlace (HEAD-relative dirty check).
func (o *Output) CommitInPlaceFrom(baseRef, message string) (bool, error) {
	clean, err := o.isClean()
	if err != nil {
		return false, err
	}
	if !clean {
		cmds := [][]string{
			{"git", "-C", o.repoPath, "add", "-A"},
			{"git", "-C", o.repoPath, "commit", "-m", message},
		}
		for _, args := range cmds {
			if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
				return false, fmt.Errorf("git %s: %w\n%s", args[3], err, out)
			}
		}
		return true, nil
	}
	// Working tree is clean. If the agent already committed on top of base,
	// there are commits ahead of baseRef — treat as a change without recommitting.
	if baseRef == "" {
		return false, nil
	}
	ahead, err := o.aheadOf(baseRef)
	if err != nil {
		return false, err
	}
	return ahead, nil
}

// aheadOf reports whether HEAD has any commits not reachable from baseRef.
func (o *Output) aheadOf(baseRef string) (bool, error) {
	out, err := exec.Command("git", "-C", o.repoPath, "rev-list", baseRef+"..HEAD").Output()
	if err != nil {
		return false, fmt.Errorf("git rev-list: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func (o *Output) isClean() (bool, error) {
	out, err := exec.Command("git", "-C", o.repoPath, "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return strings.TrimSpace(string(out)) == "", nil
}
