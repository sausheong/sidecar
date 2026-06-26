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
	harnessmem "github.com/sausheong/harness/tool/memory"
	"github.com/sausheong/harness/tools/bash"
	"github.com/sausheong/harness/tools/file"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/evaluate"
	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/notify"
	"github.com/sausheong/sidecar/internal/output"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/sausheong/sidecar/internal/triage"
	"github.com/sausheong/sidecar/internal/worktree"
)

// Task status constants.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"   // triage decided no action needed
	StatusSuggested = "suggested" // suggest-only output, no code committed
	StatusNotified  = "notified"  // notify autonomy level — notifications sent, no agent run
)

// Models holds the resolved model names for each agent role.
type Models struct {
	Coding    string
	Triage    string
	Evaluator string
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
	m.Evaluator = m.Coding
	if cfg.Models.Evaluator != "" {
		m.Evaluator = cfg.Models.Evaluator
	}
	return m
}

// ShipsCode reports whether an autonomy level results in committed code and
// therefore must pass the evaluator gate.
func ShipsCode(autonomyLevel string) bool {
	return autonomyLevel == "auto-commit" || autonomyLevel == "pull-request"
}

// GateAllowsCommit reports whether the evaluator verdict permits committing.
// Fails closed: any evaluator error blocks the commit regardless of verdict.
func GateAllowsCommit(verdict evaluate.Verdict, evalErr error) bool {
	return evalErr == nil && verdict.Pass
}

// errString returns the error message, or "" if err is nil. Used for JSON event payloads.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Loop is the core improvement loop that wraps a Harness runtime invocation
// for a single incoming Signal.
type Loop struct {
	db         *store.DB
	workspace  *store.Workspace
	cfg        *config.Config
	repoPath   string
	provider   llm.LLMProvider
	embedding  memory.EmbeddingProvider // nil when memory is not configured
	memTool    *harnessmem.MemoryTool   // nil when embedding is nil
	dispatcher *notify.Dispatcher       // nil when no notifications configured
}

// New constructs a Loop. Pass nil for embedding to disable memory retrieval and reviewer-driven memory writes.
func New(db *store.DB, workspace *store.Workspace, cfg *config.Config, repoPath string, embedding memory.EmbeddingProvider) *Loop {
	var memTool *harnessmem.MemoryTool
	if embedding != nil {
		adapter := memory.NewHarnessStoreAdapter(db, embedding, workspace.ID)
		memTool = &harnessmem.MemoryTool{Store: adapter}
	}
	return &Loop{
		db:         db,
		workspace:  workspace,
		cfg:        cfg,
		repoPath:   repoPath,
		provider:   anthropic.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), ""),
		embedding:  embedding,
		memTool:    memTool,
		dispatcher: notify.NewDispatcher(cfg.Notifications),
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
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusSkipped)
		l.dispatcher.Fire(ctx, notify.EventSkipped, sig, task)
		return nil
	}

	// ── Notify-only autonomy ─────────────────────────────────────────────────
	// When autonomy is "notify", skip the coding agent entirely and fire
	// notifications. This is the right choice for infra/network failures where
	// no code change will help — a human needs to be alerted instead.
	if tr.AutonomyLevel == "notify" {
		slog.Info("sidecar notifying (no agent run)", "task", task.ID, "change_type", tr.ChangeType)
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusNotified)
		l.dispatcher.Fire(ctx, notify.EventNotified, sig, task)
		return nil
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

	// Code-shipping autonomy levels run in an isolated worktree so concurrent
	// signals never share a working tree. suggest-only/notify run in-repo.
	workDir := l.repoPath
	var wtCleanup func() error
	var wt *worktree.Worktree
	if ShipsCode(tr.AutonomyLevel) {
		w, cleanup, wErr := worktree.Create(l.repoPath, task.ID.String())
		if wErr != nil {
			slog.Warn("worktree create failed; falling back to in-repo execution", "err", wErr, "task", task.ID)
			_ = l.db.AppendTaskEvent(ctx, task.ID, "worktree_degraded", map[string]any{"error": wErr.Error()})
		} else {
			wt, wtCleanup, workDir = w, cleanup, w.Path
			defer func() {
				if cErr := wtCleanup(); cErr != nil {
					slog.Warn("worktree cleanup failed", "err", cErr, "task", task.ID)
				}
			}()
		}
	}

	reg := tool.NewRegistry()
	reg.Register(&file.ReadFileTool{WorkDir: workDir})
	if tr.AutonomyLevel != "suggest-only" {
		reg.Register(&file.WriteFileTool{WorkDir: workDir})
		reg.Register(&file.EditFileTool{WorkDir: workDir})
		reg.Register(&bash.BashTool{WorkDir: workDir})
	}
	if l.memTool != nil {
		reg.Register(l.memTool)
	}

	systemPrompt := BuildSystemPrompt(sig)
	if memoryBlock != "" {
		systemPrompt = memoryBlock + "\n\n" + systemPrompt
	}

	taskCopy := *task // capture by value for the OnStop goroutine
	var rt *runtime.Runtime

	spec := runtime.AgentSpec{
		ID:           task.ID.String(),
		Name:         "Sidecar",
		Model:        models.Coding,
		Workspace:    workDir,
		SystemPrompt: systemPrompt,
		MaxTurns:     20,
		Loop: runtime.LoopConfig{
			Hooks: runtime.LifecycleHooks{
				OnStop: func(_ context.Context, reason string) {
					if reason != "completed" || l.memTool == nil {
						return
					}
					// Fire-and-forget reviewer. The goroutine outlives this hook
					// (and the surrounding Loop.Run, which will defer rt.Close()).
					// Safe today: Runtime.Close only releases MCP clients, and sidecar
					// has none. If sidecar later wires MCP servers via AgentSpec.MCPServers,
					// this needs a WaitGroup so Close waits for the reviewer to finish.
					go l.runReview(rt, taskCopy)
				},
			},
		},
	}

	var buildErr error
	rt, buildErr = runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: l.provider,
			Tools:    reg,
			Session:  session.NewSession(task.ID.String(), "main"),
		},
		spec,
	)
	if buildErr != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
		return fmt.Errorf("building runtime: %w", buildErr)
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
		l.dispatcher.Fire(ctx, notify.EventFailed, sig, task)
		return fmt.Errorf("agent error: %w", agentErr)
	}

	// ── Adversarial evaluation gate ──────────────────────────────────────────
	if ShipsCode(tr.AutonomyLevel) && l.cfg.VerificationEnabled() {
		verdict, evalErr := evaluate.Evaluate(ctx, l.provider, models.Evaluator, workDir, task.Summary)
		if evalErr != nil {
			slog.Warn("evaluator error; failing closed (downgrade to suggestion)", "err", evalErr, "task", task.ID)
		}
		_ = l.db.AppendTaskEvent(ctx, task.ID, "evaluation", map[string]any{
			"pass":    verdict.Pass,
			"reasons": verdict.Reasons,
			"model":   models.Evaluator,
			"error":   errString(evalErr),
		})
		if !GateAllowsCommit(verdict, evalErr) {
			slog.Info("evaluator rejected change; recording as suggestion", "task", task.ID, "reasons", verdict.Reasons)
			_ = l.db.AppendTaskEvent(ctx, task.ID, "suggestion", map[string]any{
				"summary":          textBuf.String(),
				"rejected_reasons": verdict.Reasons,
			})
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusSuggested)
			l.dispatcher.Fire(ctx, notify.EventSuggested, sig, task)
			return nil
		}
	}

	// commit creates the output branch. In a worktree it commits in place and
	// returns the worktree's branch; in-repo it falls back to CommitBranch.
	commit := func() (string, error) {
		out := output.New(workDir)
		if wt != nil {
			did, err := out.CommitInPlace("sidecar: " + task.Summary)
			if err != nil {
				return "", err
			}
			if !did {
				return output.BranchNoChanges, nil
			}
			return wt.Branch, nil
		}
		return out.CommitBranch(task.ID.String(), "sidecar: "+task.Summary)
	}

	// ── Output routing ───────────────────────────────────────────────────────
	switch tr.AutonomyLevel {
	case "suggest-only":
		summary := textBuf.String()
		_ = l.db.AppendTaskEvent(ctx, task.ID, "suggestion", map[string]any{"summary": summary})
		slog.Info("sidecar suggestion recorded", "task", task.ID, "change_type", tr.ChangeType)
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusSuggested)
		l.dispatcher.Fire(ctx, notify.EventSuggested, sig, task)
		return nil

	case "pull-request":
		branch, err := commit()
		if err != nil {
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
			l.dispatcher.Fire(ctx, notify.EventFailed, sig, task)
			return err
		}
		if branch == output.BranchNoChanges {
			slog.Info("sidecar: no changes to commit", "task", task.ID)
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)
			l.dispatcher.Fire(ctx, notify.EventCompleted, sig, task)
			return nil
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
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)
		l.dispatcher.Fire(ctx, notify.EventCompleted, sig, task)
		return nil

	default: // "auto-commit"
		branch, err := commit()
		if err != nil {
			_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusFailed)
			l.dispatcher.Fire(ctx, notify.EventFailed, sig, task)
			return err
		}
		if branch != output.BranchNoChanges {
			slog.Info("sidecar committed changes", "branch", branch, "task", task.ID)
		}
		_ = l.db.UpdateTaskStatus(ctx, task.ID, StatusCompleted)
		l.dispatcher.Fire(ctx, notify.EventCompleted, sig, task)
		return nil
	}
}

// runReview snapshots the parent runtime and extracts memory via a
// haiku reviewer with the memory tool only. Detaches from the parent
// ctx (which is canceled by the time OnStop fires) and applies its own
// 90s timeout.
func (l *Loop) runReview(parent *runtime.Runtime, task store.Task) {
	// 70s outer = 60s ReviewSpec.Timeout (capped by Review itself) +
	// ~10s headroom for GetTaskEvents and reviewer-runtime construction.
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()

	events, err := l.db.GetTaskEvents(ctx, task.ID)
	if err != nil {
		slog.Warn("review: failed to load task events", "err", err, "task", task.ID)
		return
	}

	reviewerReg := tool.NewRegistry()
	reviewerReg.Register(l.memTool)

	res := runtime.Review(ctx, parent, runtime.ReviewSpec{
		Prompt:   buildReviewerPrompt(task, events),
		Tools:    reviewerReg,
		Model:    ResolveModels(l.cfg).Triage,
		MaxTurns: 4,
		Timeout:  60 * time.Second,
	})
	if res.Err != nil {
		slog.Warn("review failed", "err", res.Err, "task", task.ID)
		return
	}
	slog.Info("review completed", "task", task.ID, "actions", len(res.Actions))
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

A CI run failed in %s:
Workflow: %s
Commit: %s
Run URL: %s

Investigate why the CI failed. Check recent changes, read failing test output if accessible,
and fix the root cause. Run tests locally to verify your fix before committing.`, base, sig.Source, workflow, sha, url)

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

	case adapter.SignalUptimeFailure:
		url, _ := sig.Payload["url"].(string)
		ft, _ := sig.Payload["failure_type"].(string)
		elapsedMs, _ := sig.Payload["elapsed_ms"].(int64)
		diagSummary, _ := sig.Payload["diagnostic_summary"].(string)

		diagBlock := ""
		if diagSummary != "" {
			diagBlock = fmt.Sprintf(`

Diagnostic results: %s

IMPORTANT — check these before touching any code:
- If dns:FAIL or tcp:FAIL → the server process or network is the problem, not code. Check if the process is running, check firewall rules, check load balancer config.
- If cross:FAIL (all endpoints down) → infrastructure outage, not a code bug. Do not commit any changes; notify the team.
- If dns:ok, tcp:ok, cross shows isolated failure → this is likely a code issue. Proceed to investigate handlers and middleware.
- If tls:FAIL → check certificate expiry; renew or update the cert configuration.
- Review any shell/http diagnostic output above for additional clues (database, cache, dependencies).`, diagSummary)
		}

		switch ft {
		case "unreachable":
			errMsg, _ := sig.Payload["error"].(string)
			return fmt.Sprintf(`%s

An uptime check detected that %s is unreachable.
Error: %s%s

Start with: bash -c 'curl -sv %s 2>&1 | head -30'
Then check if the server process is running and inspect recent git log for changes that could affect startup or routing.`, base, url, errMsg, diagBlock, url)

		case "wrong_status":
			got, _ := sig.Payload["got_status"].(int)
			want, _ := sig.Payload["expected_status"].(int)
			return fmt.Sprintf(`%s

An uptime check detected an unexpected HTTP status from %s.
Got: %d  Expected: %d%s

Start with: bash -c 'curl -sv %s 2>&1 | head -40'
Then check recent handler, middleware, and routing changes. Run the test suite to identify what is failing.`, base, url, got, want, diagBlock, url)

		case "slow_response":
			thresholdMs, _ := sig.Payload["threshold_ms"].(int)
			return fmt.Sprintf(`%s

A performance check detected that %s is responding slowly.
Response time: %dms  Threshold: %dms%s

Start with: bash -c 'curl -w "%%{time_total}" -o /dev/null -s %s'
Investigate slow database queries, missing indexes, N+1 patterns, or blocking synchronous operations on the request path. Apply a targeted fix and verify the improvement.`, base, url, elapsedMs, thresholdMs, diagBlock, url)
		}
		return fmt.Sprintf(`%s

An uptime check failed for %s.%s

Investigate the root cause starting with the diagnostic results above.`, base, url, diagBlock)

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
	case adapter.SignalUptimeFailure:
		url, _ := sig.Payload["url"].(string)
		ft, _ := sig.Payload["failure_type"].(string)
		return fmt.Sprintf("Uptime check failed for %s (%s). Investigate and fix.", url, ft)
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
	case adapter.SignalUptimeFailure:
		url, _ := sig.Payload["url"].(string)
		ft, _ := sig.Payload["failure_type"].(string)
		return fmt.Sprintf("fix uptime failure: %s (%s)", url, ft)
	default:
		desc, _ := sig.Payload["description"].(string)
		runes := []rune(desc)
		if len(runes) > 60 {
			return string(runes[:60]) + "..."
		}
		return desc
	}
}
