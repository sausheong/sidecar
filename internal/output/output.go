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
		{"git", "-C", o.repoPath, "checkout", "-b", branch},
		{"git", "-C", o.repoPath, "add", "-A"},
		{"git", "-C", o.repoPath, "commit", "-m", message},
		{"git", "-C", o.repoPath, "checkout", "-"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %s: %w\n%s", args[2], err, out)
		}
	}
	return branch, nil
}

func (o *Output) isClean() (bool, error) {
	out, err := exec.Command("git", "-C", o.repoPath, "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return strings.TrimSpace(string(out)) == "", nil
}
