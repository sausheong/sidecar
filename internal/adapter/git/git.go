package git

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
)

type GitAdapter struct {
	RepoPath     string
	PollInterval time.Duration
	lastSeen     string
	stopCh       chan struct{}
}

func New(repoPath string) *GitAdapter {
	return &GitAdapter{
		RepoPath:     repoPath,
		PollInterval: 30 * time.Second,
		stopCh:       make(chan struct{}),
	}
}

func (g *GitAdapter) Name() string { return "git" }

func (g *GitAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	// Record HEAD on startup so existing commits are not reported
	if head, err := g.headHash(); err == nil {
		g.lastSeen = head
	}

	go func() {
		ticker := time.NewTicker(g.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-g.stopCh:
				return
			case <-ticker.C:
				g.poll(out)
			}
		}
	}()
	return nil
}

func (g *GitAdapter) Stop() error {
	close(g.stopCh)
	return nil
}

func (g *GitAdapter) poll(out chan<- adapter.Signal) {
	commits, err := g.newCommits()
	if err != nil || len(commits) == 0 {
		return
	}
	for _, hash := range commits {
		out <- adapter.Signal{
			Type:   adapter.SignalGitCommit,
			Source: "git",
			Payload: map[string]any{
				"hash": hash,
				"repo": g.RepoPath,
			},
		}
	}
	g.lastSeen = commits[0]
}

func (g *GitAdapter) newCommits() ([]string, error) {
	var args []string
	if g.lastSeen == "" {
		args = []string{"-C", g.RepoPath, "log", "--format=%H", "-1"}
	} else {
		args = []string{"-C", g.RepoPath, "log", "--format=%H", g.lastSeen + "..HEAD"}
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

func (g *GitAdapter) headHash() (string, error) {
	out, err := exec.Command("git", "-C", g.RepoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
