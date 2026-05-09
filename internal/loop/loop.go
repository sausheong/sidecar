package loop

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/providers/anthropic"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tools/bash"
	"github.com/sausheong/harness/tools/file"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/output"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/sausheong/sidecar/internal/triage"
)

// Task status constants.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"   // triage decided no action needed
	StatusSuggested = "suggested" // suggest-only output, no code committed
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
	embedding memory.EmbeddingProvider // nil when memory is not configured
}

// New constructs a Loop. Pass nil for embedding to disable memory retrieval and reflect.
func New(db *store.DB, workspace *store.Workspace, cfg *config.Config, repoPath string, embedding memory.EmbeddingProvider) *Loop {
	return &Loop{
		db:        db,
		workspace: workspace,
		cfg:       cfg,
		repoPath:  repoPath,
		provider:  anthropic.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), ""),
		embedding: embedding,
	}
}

// Run executes the improvement loop for the given signal:
//  1. Creates a Task record in the database.
//  2. Runs triage to decide whether and how to act.
//  3. Builds a Harness runtime with tools gated by autonomy level.
//  4. Routes output to suggestion, PR, or auto-commit based on triage result.
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

	// ── Triage ──────────────────────────────────────────────────────────────
	models := ResolveModels(l.cfg)
	tr, err := triage.Triage(ctx, l.provider, models.Triage, sig, l.cfg)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return fmt.Errorf("triage: %w", err)
	}
	_ = l.db.AppendTaskEvent(ctx, task.ID, "triage", map[string]any{
		"should_act":     tr.ShouldAct,
		"change_type":    tr.ChangeType,
		"autonomy_level": tr.AutonomyLevel,
		"reason":         tr.Reason,
	})
	if !tr.ShouldAct {
		slog.Info("sidecar skipping signal", "reason", tr.Reason, "task", task.ID)
		return l.db.UpdateTaskStatus(ctx, task.ID, StatusSkipped)
	}

	// ── Memory retrieval ─────────────────────────────────────────────────────────
	var memoryBlock string
	if l.embedding != nil {
		block, mErr := memory.Retrieve(ctx, l.embedding, l.db, l.workspace, task.Summary, 5)
		if mErr != nil {
			slog.Warn("memory retrieval failed", "err", mErr, "task", task.ID)
		} else {
			memoryBlock = block
		}
	}

	// ── Coding agent ─────────────────────────────────────────────────────────
	if err := l.db.UpdateTaskStatus(ctx, task.ID, StatusRunning); err != nil {
		return err
	}

	reg := tool.NewRegistry()
	reg.Register(&file.ReadFileTool{WorkDir: l.repoPath})
	if tr.AutonomyLevel != "suggest-only" {
		reg.Register(&file.WriteFileTool{WorkDir: l.repoPath})
		reg.Register(&file.EditFileTool{WorkDir: l.repoPath})
		reg.Register(&bash.BashTool{WorkDir: l.repoPath})
	}

	systemPrompt := BuildSystemPrompt(sig)
	if memoryBlock != "" {
		systemPrompt = memoryBlock + "\n\n" + systemPrompt
	}

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
	var textBuf strings.Builder
	for ev := range events {
		if ev.Type == runtime.EventError {
			agentErr = ev.Error
		}
		if ev.Type == runtime.EventTextDelta {
			textBuf.WriteString(ev.Text)
		}
	}
	if agentErr != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return fmt.Errorf("agent error: %w", agentErr)
	}

	// ── Output routing ───────────────────────────────────────────────────────
	switch tr.AutonomyLevel {
	case "suggest-only":
		summary := textBuf.String()
		_ = l.db.AppendTaskEvent(ctx, task.ID, "suggestion", map[string]any{"summary": summary})
		slog.Info("sidecar suggestion recorded", "task", task.ID, "change_type", tr.ChangeType)
		task.Status = StatusSuggested
		l.launchReflect(task)
		return l.db.UpdateTaskStatus(ctx, task.ID, StatusSuggested)

	case "pull-request":
		out := output.New(l.repoPath)
		branch, err := out.CommitBranch(task.ID.String(), "sidecar: "+task.Summary)
		if err != nil {
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
			return err
		}
		if branch == output.BranchNoChanges {
			slog.Info("sidecar: no changes to commit", "task", task.ID)
			task.Status = StatusCompleted
			l.launchReflect(task)
			return l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)
		}
		repo, token := l.resolveRepoAndToken(sig)
		if repo != "" {
			pc := output.NewPRCreator(l.repoPath, repo, token)
			prURL, prErr := pc.Create(branch, "sidecar: "+task.Summary, l.prBody(sig, tr, task.ID.String()))
			if prErr != nil {
				slog.Warn("sidecar PR creation failed", "err", prErr, "branch", branch)
			} else {
				_ = l.db.AppendTaskEvent(ctx, task.ID, "pr_created", map[string]any{"url": prURL})
				slog.Info("sidecar PR created", "url", prURL, "task", task.ID)
			}
		}
		task.Status = StatusCompleted
		l.launchReflect(task)
		return l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)

	default: // "auto-commit"
		out := output.New(l.repoPath)
		branch, err := out.CommitBranch(task.ID.String(), "sidecar: "+task.Summary)
		if err != nil {
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
			return err
		}
		if branch != output.BranchNoChanges {
			slog.Info("sidecar committed changes", "branch", branch, "task", task.ID)
		}
		task.Status = StatusCompleted
		l.launchReflect(task)
		return l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)
	}
}

// launchReflect starts an async goroutine to extract memory from a completed task.
func (l *Loop) launchReflect(task *store.Task) {
	if l.embedding == nil {
		return
	}
	taskCopy := *task // copy before goroutine captures it to avoid future data-race risk
	go func() {
		reflectCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		events, err := l.db.GetTaskEvents(reflectCtx, taskCopy.ID)
		if err != nil {
			slog.Warn("reflect: failed to load task events", "err", err, "task", taskCopy.ID)
			return
		}
		models := ResolveModels(l.cfg)
		if err := memory.Reflect(reflectCtx, l.embedding, l.provider, models.Triage, l.db, l.workspace, &taskCopy, events); err != nil {
			slog.Warn("reflect failed", "err", err, "task", taskCopy.ID)
		}
	}()
}

// resolveRepoAndToken looks up the repo slug and resolved token for a signal.
func (l *Loop) resolveRepoAndToken(sig adapter.Signal) (repo, token string) {
	if r, ok := sig.Payload["repo"].(string); ok && r != "" {
		repo = r
	}
	for _, sc := range l.cfg.Signals {
		if sc.Repo == repo {
			token = sc.ResolveToken()
			return
		}
	}
	token = os.Getenv("GITHUB_TOKEN")
	return
}

// prBody generates the PR description.
func (l *Loop) prBody(sig adapter.Signal, tr triage.TriageResult, taskID string) string {
	return fmt.Sprintf("## Sidecar automated fix\n\n**Signal:** %s — %s\n**Change type:** %s\n**Task ID:** %s\n\nThis PR was created automatically by Sidecar. Review and merge if the fix looks correct.",
		string(sig.Type), summarize(sig), tr.ChangeType, taskID)
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

	case adapter.SignalCIFailure:
		workflow, _ := sig.Payload["workflow_name"].(string)
		sha, _ := sig.Payload["head_sha"].(string)
		url, _ := sig.Payload["html_url"].(string)
		return fmt.Sprintf(`%s

A GitHub Actions CI run failed:
Workflow: %s
Commit: %s
Run URL: %s

Investigate why the CI failed. Check recent changes, read failing test output if accessible,
and fix the root cause. Run tests locally to verify your fix before committing.`, base, workflow, sha, url)

	case adapter.SignalScheduleTick:
		return fmt.Sprintf(`%s

This is a proactive maintenance sweep. Look for improvement opportunities:
- Fix or improve any area flagged as fragile or prone to regression in the workspace memory above
- Add tests to code paths noted as missing coverage
- If no specific area is flagged, check for: stale dependencies, dead code, or outdated docs

Pick ONE meaningful improvement and apply it. Run tests to verify your change.`, base)

	case adapter.SignalLogAnomaly:
		pattern, _ := sig.Payload["pattern"].(string)
		source, _ := sig.Payload["source"].(string)
		line, _ := sig.Payload["line"].(string)
		return fmt.Sprintf(`%s

A log anomaly was detected in the running application.

Pattern: %s
Source:  %s
Sample:  %s

Investigate the root cause. Check relevant code paths, reproduce the issue if possible,
and apply a fix. Run tests to verify your change.`, base, pattern, source, line)

	case adapter.SignalMetricAlert:
		name, _ := sig.Payload["alert_name"].(string)
		message, _ := sig.Payload["message"].(string)
		provider, _ := sig.Payload["provider"].(string)
		return fmt.Sprintf(`%s

A metrics alert has fired in the %s monitoring system.

Alert:   %s
Details: %s

Investigate the root cause in the codebase. Check recent changes, identify the code
path responsible, and apply a fix. Run tests to verify your change.`, base, provider, name, message)

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
	case adapter.SignalCIFailure:
		workflow, _ := sig.Payload["workflow_name"].(string)
		sha, _ := sig.Payload["head_sha"].(string)
		return fmt.Sprintf("CI failure in workflow %q on commit %s. Investigate and fix.", workflow, sha)
	case adapter.SignalScheduleTick:
		return "Proactive sweep: identify and apply one meaningful improvement."
	case adapter.SignalLogAnomaly:
		pattern, _ := sig.Payload["pattern"].(string)
		source, _ := sig.Payload["source"].(string)
		return fmt.Sprintf("Log anomaly detected: pattern %q in %s. Investigate and fix.", pattern, source)
	case adapter.SignalMetricAlert:
		name, _ := sig.Payload["alert_name"].(string)
		provider, _ := sig.Payload["provider"].(string)
		return fmt.Sprintf("Metrics alert %q fired in %s. Investigate and fix.", name, provider)
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
	case adapter.SignalCIFailure:
		workflow, _ := sig.Payload["workflow_name"].(string)
		sha, _ := sig.Payload["head_sha"].(string)
		if len(sha) > 8 {
			sha = sha[:8]
		}
		return fmt.Sprintf("fix CI failure in %s @ %s", workflow, sha)
	case adapter.SignalScheduleTick:
		return "proactive sweep"
	case adapter.SignalLogAnomaly:
		pattern, _ := sig.Payload["pattern"].(string)
		source, _ := sig.Payload["source"].(string)
		return fmt.Sprintf("fix log anomaly: %s in %s", pattern, source)
	case adapter.SignalMetricAlert:
		name, _ := sig.Payload["alert_name"].(string)
		return fmt.Sprintf("fix metric alert: %s", name)
	default:
		desc, _ := sig.Payload["description"].(string)
		runes := []rune(desc)
		if len(runes) > 60 {
			return string(runes[:60]) + "..."
		}
		return desc
	}
}
