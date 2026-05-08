package loop

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/providers/anthropic"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tools/bash"
	"github.com/sausheong/harness/tools/file"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/output"
	"github.com/sausheong/sidecar/internal/store"
)

// Task status constants.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// Models holds the resolved model names for each agent role.
type Models struct {
	Coding string
	Triage string
}

// ResolveModels returns the effective model names from cfg, falling back to
// built-in defaults when the config fields are empty.
func ResolveModels(cfg *config.Config) Models {
	m := Models{
		Coding: "anthropic/claude-sonnet-4-6",
		Triage: "anthropic/claude-haiku-4-5-20251001",
	}
	if cfg.Models.Coding != "" {
		m.Coding = cfg.Models.Coding
	}
	if cfg.Models.Triage != "" {
		m.Triage = cfg.Models.Triage
	}
	return m
}

// Loop is the core improvement loop that wraps a Harness runtime invocation
// for a single incoming Signal.
type Loop struct {
	db        *store.DB
	workspace *store.Workspace
	cfg       *config.Config
	repoPath  string
	provider  llm.LLMProvider
}

// New constructs a Loop bound to the given database, workspace, config, and repo path.
// The Anthropic API key is resolved once at construction time.
func New(db *store.DB, workspace *store.Workspace, cfg *config.Config, repoPath string) *Loop {
	return &Loop{
		db:        db,
		workspace: workspace,
		cfg:       cfg,
		repoPath:  repoPath,
		provider:  anthropic.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), ""),
	}
}

// Run executes the improvement loop for the given signal:
//  1. Creates a Task record in the database.
//  2. Builds a Harness runtime with file+bash tools.
//  3. Runs the agent until it finishes (drains the event channel).
//  4. Commits any filesystem changes to a sidecar/<taskID> branch.
//  5. Updates task status in the database.
func (l *Loop) Run(ctx context.Context, sig adapter.Signal) error {
	task := &store.Task{
		WorkspaceID: l.workspace.ID,
		SignalType:  string(sig.Type),
		Summary:     summarize(sig),
	}
	if err := l.db.CreateTask(ctx, task); err != nil {
		return fmt.Errorf("creating task: %w", err)
	}

	if err := l.db.UpdateTaskStatus(ctx, task.ID, StatusRunning); err != nil {
		return err
	}

	models := ResolveModels(l.cfg)
	systemPrompt := BuildSystemPrompt(sig)

	reg := tool.NewRegistry()
	reg.Register(&file.ReadFileTool{WorkDir: l.repoPath})
	reg.Register(&file.WriteFileTool{WorkDir: l.repoPath})
	reg.Register(&file.EditFileTool{WorkDir: l.repoPath})
	reg.Register(&bash.BashTool{WorkDir: l.repoPath})

	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: l.provider,
			Tools:    reg,
			Session:  session.NewSession(task.ID.String(), "main"),
		},
		runtime.AgentSpec{
			ID:           task.ID.String(),
			Name:         "Sidecar",
			Model:        models.Coding,
			Workspace:    l.repoPath,
			SystemPrompt: systemPrompt,
			MaxTurns:     20,
		},
	)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return fmt.Errorf("building runtime: %w", err)
	}
	defer rt.Close()

	events, err := rt.Run(ctx, userMessage(sig), nil)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return err
	}

	var agentErr error
	for ev := range events {
		if ev.Type == runtime.EventError {
			agentErr = ev.Error
		}
	}
	if agentErr != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return fmt.Errorf("agent error: %w", agentErr)
	}

	out := output.New(l.repoPath)
	branch, err := out.CommitBranch(task.ID.String(), "sidecar: "+task.Summary)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return err
	}

	if branch != "" {
		slog.Info("sidecar committed changes", "branch", branch, "task", task.ID)
	}
	return l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)
}

// BuildSystemPrompt constructs the agent system prompt based on the signal type.
func BuildSystemPrompt(sig adapter.Signal) string {
	base := `You are an autonomous engineering agent (Sidecar) attached to a software project.
Your job is to improve, fix, and maintain the codebase. You have access to the filesystem and bash.
Make targeted, minimal changes. Run tests after any code change to verify correctness.
Only modify files relevant to the current task.`

	switch sig.Type {
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		return fmt.Sprintf(`%s

A new commit (%s) was just pushed. Review it and fix any immediate issues:
broken tests, compilation errors, obvious bugs introduced by the change.
If everything looks good, do nothing.`, base, hash)

	case adapter.SignalScheduleTick:
		return fmt.Sprintf(`%s

This is a proactive maintenance sweep. Look for improvement opportunities:
- Stale or vulnerable dependencies
- Missing test coverage for existing code paths
- Outdated documentation
- Dead code or unused imports
Pick one meaningful improvement and apply it.`, base)

	default:
		desc, _ := sig.Payload["description"].(string)
		return fmt.Sprintf(`%s

On-demand task: %s

Complete this task. Run tests to verify your changes.`, base, desc)
	}
}

// userMessage returns the initial user turn sent to the agent.
func userMessage(sig adapter.Signal) string {
	switch sig.Type {
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		return fmt.Sprintf("New commit detected: %s. Review and fix any issues.", hash)
	case adapter.SignalScheduleTick:
		return "Proactive sweep: identify and apply one meaningful improvement."
	default:
		desc, _ := sig.Payload["description"].(string)
		if desc == "" {
			return "Perform a general codebase health check and fix any obvious issues."
		}
		return desc
	}
}

// summarize produces a short human-readable summary of the signal for storage.
func summarize(sig adapter.Signal) string {
	switch sig.Type {
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		if len(hash) > 8 {
			hash = hash[:8]
		}
		return "review commit " + hash
	case adapter.SignalScheduleTick:
		return "proactive sweep"
	default:
		desc, _ := sig.Payload["description"].(string)
		runes := []rune(desc)
		if len(runes) > 60 {
			return string(runes[:60]) + "..."
		}
		return desc
	}
}
