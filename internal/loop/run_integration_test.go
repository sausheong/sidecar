//go:build integration

package loop_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/llm/llmtest"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dbURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("SIDECAR_TEST_DB_URL")
	if u == "" {
		t.Skip("SIDECAR_TEST_DB_URL not set")
	}
	return u
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "t@t.com"},
		{"git", "-C", dir, "config", "user.name", "T"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}

// scriptedProvider is a single llm.LLMProvider that serves all three agent
// roles within one Loop.Run by inspecting each ChatRequest's system prompt:
//
//   - triage   (prompt contains "triage agent")          -> JSON classification
//   - evaluator(prompt contains "adversarial code reviewer") -> JSON verdict
//   - coding   (everything else, i.e. Sidecar's BuildSystemPrompt) -> a
//     write_file tool call (so there's a real diff in the worktree), then a
//     terminal stop once the tool result is in the history.
//
// Routing is by system prompt, not model, because the sidecar default coding
// and evaluator models are identical (ResolveModels sets Evaluator = Coding),
// so model name alone cannot distinguish the coding turn from the evaluator
// turn. The system prompts are distinct and survive verbatim into
// req.SystemPromptParts (BuildStaticSystemPrompt embeds AgentSpec.SystemPrompt
// unchanged), so substring matching on the prompt is robust.
//
// Every terminal EventDone carries a small *Usage so the loop's usage
// recording (AccumulateUsage / SumWorkspaceTokensSince) is exercised.
type scriptedProvider struct {
	llmtest.Base
	evalPass bool // verdict returned for the evaluator turn
	evalErr  bool // when true, the evaluator turn returns an error (fail-closed test)
}

// systemText returns the effective system prompt for routing. The runtime
// sends the prompt via SystemPromptParts; fall back to SystemPrompt for safety.
func systemText(req llm.ChatRequest) string {
	if len(req.SystemPromptParts) > 0 {
		return llm.JoinSystemPromptParts(req.SystemPromptParts)
	}
	return req.SystemPrompt
}

// hasWriteFileResult reports whether the conversation already contains the
// result of a write_file tool call — i.e. the coding agent has already written
// the file and the next coding turn should terminate.
func hasWriteFileResult(req llm.ChatRequest) bool {
	for _, m := range req.Messages {
		if m.ToolCallID != "" {
			return true
		}
	}
	return false
}

func usageEvent() llm.ChatEvent {
	return llm.ChatEvent{Type: llm.EventDone, Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}}
}

func (p *scriptedProvider) ChatStream(_ context.Context, req llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	sys := systemText(req)

	// Evaluator role: optionally fail closed, otherwise emit a JSON verdict.
	if strings.Contains(sys, "adversarial code reviewer") {
		if p.evalErr {
			ch := make(chan llm.ChatEvent, 1)
			go func() {
				defer close(ch)
				ch <- llm.ChatEvent{Type: llm.EventError, Error: errEvaluator}
			}()
			return ch, nil
		}
		verdict := `{"pass": false, "reasons": "rejected by test"}`
		if p.evalPass {
			verdict = `{"pass": true, "reasons": "looks good"}`
		}
		return emit(verdict), nil
	}

	// Triage role: classify as an actionable bug_fix so the coding agent runs.
	if strings.Contains(sys, "triage agent") {
		return emit(`{"should_act": true, "change_type": "bug_fix", "reason": "test signal"}`), nil
	}

	// Coding role: write a file on the first turn (creating a real diff), then
	// stop on the next turn once the tool result is in the history.
	if hasWriteFileResult(req) {
		ch := make(chan llm.ChatEvent, 2)
		go func() {
			defer close(ch)
			ch <- llm.ChatEvent{Type: llm.EventTextDelta, Text: "done writing the fix"}
			ch <- usageEvent()
		}()
		return ch, nil
	}
	tc := &llm.ToolCall{
		ID:    "tc_write_1",
		Name:  "write_file",
		Input: []byte(`{"path": "sidecar_fix.txt", "content": "scripted fix\n"}`),
	}
	ch := make(chan llm.ChatEvent, 3)
	go func() {
		defer close(ch)
		ch <- llm.ChatEvent{Type: llm.EventToolCallStart, ToolCall: tc}
		ch <- llm.ChatEvent{Type: llm.EventToolCallDone, ToolCall: tc}
		ch <- usageEvent()
	}()
	return ch, nil
}

// emit returns a channel that streams text then a terminal usage event.
func emit(text string) <-chan llm.ChatEvent {
	ch := make(chan llm.ChatEvent, 2)
	go func() {
		defer close(ch)
		ch <- llm.ChatEvent{Type: llm.EventTextDelta, Text: text}
		ch <- usageEvent()
	}()
	return ch
}

// errEvaluator is the error surfaced on the evaluator turn for the
// fail-closed case.
var errEvaluator = evalError("scripted evaluator failure")

type evalError string

func (e evalError) Error() string { return string(e) }

func newLoop(t *testing.T, repo string, p llm.LLMProvider, cfg *config.Config) (*loop.Loop, *store.DB, *store.Workspace) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Connect(ctx, dbURL(t))
	require.NoError(t, err)
	require.NoError(t, store.Migrate(ctx, db))
	ws := &store.Workspace{Name: "itest", Path: repo, ConfigHash: "h"}
	require.NoError(t, db.UpsertWorkspace(ctx, ws))
	l := loop.New(db, ws, cfg, repo, nil)
	loop.SetProviderForTest(l, p) // inject scripted provider
	return l, db, ws
}

func bugFixCfg() *config.Config {
	tr := true
	return &config.Config{
		Autonomy:     config.AutonomyPolicy{BugFixes: "auto-commit"},
		Verification: config.VerificationConfig{Enabled: &tr},
	}
}

func gitCommitSignal() adapter.Signal {
	return adapter.Signal{Type: adapter.SignalGitCommit, Source: "git", Payload: map[string]any{"hash": "abc123"}}
}

func lastStatus(t *testing.T, db *store.DB, ws *store.Workspace) string {
	tasks, err := db.ListTasks(context.Background(), ws.ID, 1)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	return tasks[0].Status
}

func TestRun_PassCommits(t *testing.T) {
	repo := initRepo(t)
	l, db, ws := newLoop(t, repo, &scriptedProvider{evalPass: true}, bugFixCfg())
	require.NoError(t, l.Run(context.Background(), gitCommitSignal()))
	assert.Equal(t, loop.StatusCompleted, lastStatus(t, db, ws))
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "sidecar/*").Output()
	assert.NotEmpty(t, strings.TrimSpace(string(out)), "a sidecar branch should exist")
}

func TestRun_RejectSuggests(t *testing.T) {
	repo := initRepo(t)
	l, db, ws := newLoop(t, repo, &scriptedProvider{evalPass: false}, bugFixCfg())
	require.NoError(t, l.Run(context.Background(), gitCommitSignal()))
	assert.Equal(t, loop.StatusSuggested, lastStatus(t, db, ws))
}

func TestRun_EvaluatorErrorFailsClosed(t *testing.T) {
	repo := initRepo(t)
	l, db, ws := newLoop(t, repo, &scriptedProvider{evalErr: true}, bugFixCfg())
	require.NoError(t, l.Run(context.Background(), gitCommitSignal()))
	assert.Equal(t, loop.StatusSuggested, lastStatus(t, db, ws))
}

func TestRun_BudgetExceededSkips(t *testing.T) {
	repo := initRepo(t)
	cfg := bugFixCfg()
	cfg.Budget = config.BudgetConfig{DailyTokens: 100}
	l, db, ws := newLoop(t, repo, &scriptedProvider{evalPass: true}, cfg)
	// Pre-seed usage over budget on a throwaway task.
	seed := &store.Task{WorkspaceID: ws.ID, SignalType: "git.commit", Summary: "seed"}
	require.NoError(t, db.CreateTask(context.Background(), seed))
	require.NoError(t, db.AppendTaskEvent(context.Background(), seed.ID, "usage", map[string]any{"total": 1000}))
	require.NoError(t, l.Run(context.Background(), gitCommitSignal()))
	assert.Equal(t, loop.StatusSkipped, lastStatus(t, db, ws))
}
