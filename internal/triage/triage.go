package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
)

// TriageResult is the structured output of the triage agent.
type TriageResult struct {
	ShouldAct     bool   // false = skip this signal entirely
	ChangeType    string // "test_fix" | "bug_fix" | "dependency_update" | "refactor" | "log_fix" | "metric_fix" | "unknown"
	AutonomyLevel string // "auto-commit" | "pull-request" | "suggest-only"
	Reason        string // one-sentence explanation
}

const triageSystemPrompt = `You are a triage agent for an autonomous software engineering system.
Analyze the incoming signal and decide:
1. Whether it warrants autonomous action (should_act: true/false)
2. If so, what type of change is needed

Valid change_type values: "test_fix", "bug_fix", "dependency_update", "refactor", "log_fix", "metric_fix", "unknown"

Do NOT act (should_act: false) for:
- CI failures caused by infrastructure issues (network timeouts, disk full, runner unavailable)
- Documentation-only commits that cannot cause test failures
- Signals with insufficient information to determine a fix

Respond with ONLY valid JSON, no prose:
{"should_act": true, "change_type": "test_fix", "reason": "one sentence"}`

// Triage calls the triage model to classify a signal.
// On any failure it returns a conservative default (suggest-only) rather than dropping the signal.
func Triage(ctx context.Context, provider llm.LLMProvider, model string, sig adapter.Signal, cfg *config.Config) (TriageResult, error) {
	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: provider,
			Tools:    tool.NewRegistry(),
			Session:  session.NewSession("triage-"+uuid.New().String(), "main"),
		},
		runtime.AgentSpec{
			ID:           "triage",
			Name:         "Triage",
			Model:        model,
			SystemPrompt: triageSystemPrompt,
			MaxTurns:     1,
		},
	)
	if err != nil {
		slog.Warn("triage runtime build failed, defaulting to suggest-only", "err", err)
		return conservativeDefault(), nil
	}
	defer rt.Close()

	events, err := rt.Run(ctx, BuildTriageMessage(sig), nil)
	if err != nil {
		slog.Warn("triage run failed, defaulting to suggest-only", "err", err)
		return conservativeDefault(), nil
	}

	var sb strings.Builder
	for ev := range events {
		if ev.Type == runtime.EventTextDelta {
			sb.WriteString(ev.Text)
		}
	}

	result, err := ParseTriageResponse(sb.String())
	if err != nil {
		slog.Warn("triage response parse failed, defaulting to suggest-only", "err", err, "raw", sb.String())
		return conservativeDefault(), nil
	}

	result.AutonomyLevel = ResolveAutonomy(result.ChangeType, cfg)
	return result, nil
}

// BuildTriageMessage constructs the user-turn message sent to the triage agent.
func BuildTriageMessage(sig adapter.Signal) string {
	switch sig.Type {
	case adapter.SignalCIFailure:
		workflow, _ := sig.Payload["workflow_name"].(string)
		conclusion, _ := sig.Payload["conclusion"].(string)
		sha, _ := sig.Payload["head_sha"].(string)
		url, _ := sig.Payload["html_url"].(string)
		repo, _ := sig.Payload["repo"].(string)
		isFlake, _ := sig.Payload["is_flake"].(bool)
		return fmt.Sprintf("CI failure in %s:\nWorkflow: %s\nConclusion: %s\nCommit: %s\nURL: %s\nRepo: %s\nFlaky: %v\n\nShould this be fixed automatically?",
			sig.Source, workflow, conclusion, sha, url, repo, isFlake)
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		return fmt.Sprintf("New git commit: %s\nShould this commit be reviewed and fixed if it introduced issues?", hash)
	case adapter.SignalScheduleTick:
		return "Scheduled maintenance sweep. Should a proactive improvement be made to the codebase?"
	case adapter.SignalLogAnomaly:
		pattern, _ := sig.Payload["pattern"].(string)
		source, _ := sig.Payload["source"].(string)
		line, _ := sig.Payload["line"].(string)
		return fmt.Sprintf("Log anomaly detected:\nPattern: %s\nSource: %s\nSample: %s\n\nShould this be investigated and fixed automatically?",
			pattern, source, line)
	case adapter.SignalMetricAlert:
		name, _ := sig.Payload["alert_name"].(string)
		message, _ := sig.Payload["message"].(string)
		provider, _ := sig.Payload["provider"].(string)
		return fmt.Sprintf("Metrics alert fired in %s:\nAlert: %s\nDetails: %s\n\nShould this be investigated and fixed automatically?",
			provider, name, message)
	case adapter.SignalUptimeFailure:
		url, _ := sig.Payload["url"].(string)
		ft, _ := sig.Payload["failure_type"].(string)
		diagSummary, _ := sig.Payload["diagnostic_summary"].(string)
		diagNote := ""
		if diagSummary != "" {
			diagNote = fmt.Sprintf("\nDiagnostics: %s", diagSummary)
		}
		switch ft {
		case "unreachable":
			errMsg, _ := sig.Payload["error"].(string)
			return fmt.Sprintf("Uptime check failed: %s is unreachable.\nError: %s%s\n\nIf diagnostics show DNS/TCP/network failures or all endpoints are down, this is likely infrastructure — do NOT act. If network is healthy and failure is isolated to this endpoint, investigate the code.", url, errMsg, diagNote)
		case "wrong_status":
			got, _ := sig.Payload["got_status"].(int)
			want, _ := sig.Payload["expected_status"].(int)
			return fmt.Sprintf("Uptime check failed: %s returned HTTP %d (expected %d).%s\n\nIf other endpoints are healthy and DNS/TCP pass, this is likely a code issue — act. If network checks fail or all endpoints are down, this is infrastructure — do NOT act.", url, got, want, diagNote)
		case "slow_response":
			ms, _ := sig.Payload["elapsed_ms"].(int64)
			threshold, _ := sig.Payload["threshold_ms"].(int)
			return fmt.Sprintf("Performance degradation: %s responded in %dms (threshold: %dms).%s\n\nIf other endpoints are also slow or infrastructure checks fail, this may be a resource/network issue. If isolated to this endpoint, investigate slow queries or blocking operations.", url, ms, threshold, diagNote)
		}
		return fmt.Sprintf("Uptime check failed for %s.%s Should this be investigated automatically?", url, diagNote)
	default:
		desc, _ := sig.Payload["description"].(string)
		return fmt.Sprintf("On-demand task: %s\nShould this task be executed?", desc)
	}
}

// ParseTriageResponse unmarshals the triage agent's JSON response.
func ParseTriageResponse(raw string) (TriageResult, error) {
	raw = strings.TrimSpace(raw)
	// Strip markdown code fences the LLM sometimes wraps around JSON.
	if strings.HasPrefix(raw, "```") {
		if idx := strings.Index(raw, "\n"); idx != -1 {
			raw = raw[idx+1:]
		}
		if idx := strings.LastIndex(raw, "```"); idx != -1 {
			raw = raw[:idx]
		}
		raw = strings.TrimSpace(raw)
	}
	var resp struct {
		ShouldAct  bool   `json:"should_act"`
		ChangeType string `json:"change_type"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return TriageResult{}, fmt.Errorf("parsing triage JSON: %w", err)
	}
	return TriageResult{
		ShouldAct:  resp.ShouldAct,
		ChangeType: resp.ChangeType,
		Reason:     resp.Reason,
	}, nil
}

// ResolveAutonomy maps a change type to an autonomy level using the config.
// Unknown or unmapped types fall back to "suggest-only".
func ResolveAutonomy(changeType string, cfg *config.Config) string {
	var level string
	switch changeType {
	case "test_fix":
		level = cfg.Autonomy.TestFixes
	case "bug_fix":
		level = cfg.Autonomy.BugFixes
	case "dependency_update":
		level = cfg.Autonomy.DependencyUpdates
	case "refactor":
		level = cfg.Autonomy.Refactoring
	case "log_fix":
		level = cfg.Autonomy.LogFixes
	case "metric_fix":
		level = cfg.Autonomy.MetricFixes
	case "uptime_fix":
		level = cfg.Autonomy.UptimeFixes
	}
	if level == "" {
		return "suggest-only"
	}
	return level
}

func conservativeDefault() TriageResult {
	return TriageResult{
		ShouldAct:     true,
		ChangeType:    "unknown",
		AutonomyLevel: "suggest-only",
		Reason:        "triage failed, defaulting to suggest-only",
	}
}
