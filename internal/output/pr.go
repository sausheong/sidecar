package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const githubAPIBase = "https://api.github.com"

// PRCreator creates GitHub pull requests by trying gh CLI first,
// then falling back to the GitHub REST API.
type PRCreator struct {
	repoPath string // local git repository path
	repoSlug string // "owner/repo"
	token    string // GitHub personal access token
	baseURL  string // GitHub API base URL (overridable for tests)
	client   *http.Client
}

// NewPRCreator creates a PRCreator using the real GitHub API.
func NewPRCreator(repoPath, repoSlug, token string) *PRCreator {
	return NewPRCreatorWithBaseURL(repoPath, repoSlug, token, githubAPIBase)
}

// NewPRCreatorWithBaseURL creates a PRCreator with a custom API base URL (used in tests).
func NewPRCreatorWithBaseURL(repoPath, repoSlug, token, baseURL string) *PRCreator {
	return &PRCreator{
		repoPath: repoPath,
		repoSlug: repoSlug,
		token:    token,
		baseURL:  baseURL,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Create pushes branch to origin and opens a PR. Returns the PR HTML URL.
func (p *PRCreator) Create(branch, title, body string) (string, error) {
	if err := p.pushBranch(branch); err != nil {
		return "", fmt.Errorf("pushing branch: %w", err)
	}

	base := p.DefaultBranch()

	// Try gh CLI first
	if url, err := p.createViaGH(branch, base, title, body); err == nil {
		return url, nil
	}

	// Fallback: GitHub REST API
	return p.createViaAPI(branch, base, title, body)
}

// DefaultBranch returns the default branch of the remote (e.g. "main" or "master").
func (p *PRCreator) DefaultBranch() string {
	out, err := exec.Command("git", "-C", p.repoPath,
		"symbolic-ref", "refs/remotes/origin/HEAD", "--short").Output()
	if err != nil {
		return "main"
	}
	branch := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(branch, "/"); idx >= 0 {
		branch = branch[idx+1:]
	}
	if branch == "" {
		return "main"
	}
	return branch
}

func (p *PRCreator) pushBranch(branch string) error {
	if p.baseURL != githubAPIBase {
		// Test mode: push to local origin directly (no auth needed).
		out, err := exec.Command("git", "-C", p.repoPath, "push", "origin", branch).CombinedOutput()
		if err != nil {
			return fmt.Errorf("git push: %w\n%s", err, out)
		}
		return nil
	}

	// Production: use GIT_ASKPASS to avoid embedding the token in the process command line.
	askpass, err := writeTempAskpass(p.token)
	if err != nil {
		return fmt.Errorf("creating askpass: %w", err)
	}
	defer os.Remove(askpass)

	remoteURL := fmt.Sprintf("https://github.com/%s.git", p.repoSlug)
	cmd := exec.Command("git", "-C", p.repoPath, "push", remoteURL, branch)
	cmd.Env = append(os.Environ(),
		"GIT_ASKPASS="+askpass,
		"GIT_TERMINAL_PROMPT=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %w\n%s", err, out)
	}
	return nil
}

// writeTempAskpass writes a temporary executable script that echoes the token
// when git asks for a password. Returns the script path.
func writeTempAskpass(token string) (string, error) {
	f, err := os.CreateTemp("", "sidecar-askpass-*.sh")
	if err != nil {
		return "", err
	}
	defer f.Close()

	// The script echoes the token regardless of which credential field git asks for.
	script := fmt.Sprintf("#!/bin/sh\necho '%s'\n", token)
	if _, err := f.WriteString(script); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if err := os.Chmod(f.Name(), 0700); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (p *PRCreator) createViaGH(branch, base, title, body string) (string, error) {
	out, err := exec.Command("gh", "pr", "create",
		"--repo", p.repoSlug,
		"--head", branch,
		"--base", base,
		"--title", title,
		"--body", body,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (p *PRCreator) createViaAPI(branch, base, title, body string) (string, error) {
	payload := map[string]string{
		"title": title,
		"body":  body,
		"head":  branch,
		"base":  base,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling pr payload: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/pulls", p.baseURL, p.repoSlug)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding github response: %w", err)
	}
	return result.HTMLURL, nil
}
