// Package evaluate implements the generator/evaluator split: a fresh skeptic
// runtime reviews the generator's diff and can REJECT it before any commit.
// This closes the "Nodding Loop" — an agent grading its own work.
package evaluate

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tools/bash"
	"github.com/sausheong/harness/tools/file"
)

// Verdict is the evaluator's decision on a diff.
type Verdict struct {
	Pass    bool
	Reasons string
}

const evaluatorSystemPrompt = `You are an adversarial code reviewer for an autonomous engineering system.
A generator agent just produced a diff. ASSUME THIS DIFF IS BROKEN until proven otherwise.
Do NOT praise. Do NOT trust the author's intent. Your job is to find what fails.

Check, in order:
1. Does it build/compile? Run the build.
2. Do the tests pass? Run them — do not just read them.
3. Edge cases the author skipped.
4. Does the behaviour actually match the stated task?

You have read and shell access. Use them to verify by acting, not by reading.

Respond with ONLY valid JSON, no prose:
{"pass": false, "reasons": "one or two sentences citing the specific failure"}
Set pass=true ONLY if every check above holds.`

// SystemPrompt returns the evaluator system prompt. Exposed for tests.
func SystemPrompt() string { return evaluatorSystemPrompt }

// BuildEvalMessage builds the user-turn message: the task plus the diff to judge.
func BuildEvalMessage(taskSummary, diff string) string {
	return fmt.Sprintf(`Task the generator was asked to do: %s

Here is the generator's uncommitted diff. Verify it by building and running tests, then return your verdict.

--- DIFF ---
%s
--- END DIFF ---`, taskSummary, diff)
}

// ParseVerdict extracts the JSON verdict from the evaluator's response,
// tolerating code fences and surrounding prose.
func ParseVerdict(raw string) (Verdict, error) {
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "{"); start != -1 {
		if end := strings.LastIndex(raw, "}"); end != -1 && end >= start {
			raw = raw[start : end+1]
		}
	}
	var resp struct {
		Pass    bool   `json:"pass"`
		Reasons string `json:"reasons"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return Verdict{}, fmt.Errorf("parsing verdict JSON: %w", err)
	}
	return Verdict{Pass: resp.Pass, Reasons: resp.Reasons}, nil
}

// Evaluate builds a fresh skeptic runtime over the diff in workDir and returns
// its verdict. It does NOT inherit the generator's conversation — the evaluator
// must carry none of the generator's self-persuasion.
//
// The diff is taken against baseRef so it captures BOTH uncommitted changes and
// any commits the generator agent made on top of base (the agent has bash and
// can self-commit; diffing against HEAD would then yield an empty diff and
// silently bypass this gate). If baseRef is "" it falls back to "HEAD" to
// preserve the in-repo (non-worktree) behavior.
//
// On any setup/run error the caller should fail closed (treat as REJECT); this
// function returns the error so the caller can record it.
func Evaluate(ctx context.Context, provider llm.LLMProvider, model, workDir, baseRef, taskSummary string) (Verdict, error) {
	if baseRef == "" {
		baseRef = "HEAD"
	}
	diffOut, err := exec.Command("git", "-C", workDir, "diff", baseRef).CombinedOutput()
	if err != nil {
		return Verdict{}, fmt.Errorf("git diff: %w\n%s", err, strings.TrimSpace(string(diffOut)))
	}
	diff := strings.TrimSpace(string(diffOut))
	if diff == "" {
		// No diff to judge — nothing was shipped; treat as a trivial pass.
		return Verdict{Pass: true, Reasons: "no changes to evaluate"}, nil
	}

	reg := tool.NewRegistry()
	reg.Register(&file.ReadFileTool{WorkDir: workDir})
	reg.Register(&bash.BashTool{WorkDir: workDir})

	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: provider,
			Tools:    reg,
			Session:  session.NewSession("evaluate-"+sessionSuffix(taskSummary), "main"),
		},
		runtime.AgentSpec{
			ID:           "evaluate",
			Name:         "Evaluator",
			Model:        model,
			Workspace:    workDir,
			SystemPrompt: evaluatorSystemPrompt,
			MaxTurns:     8,
		},
	)
	if err != nil {
		return Verdict{}, fmt.Errorf("building evaluator runtime: %w", err)
	}
	defer rt.Close()

	events, err := rt.Run(ctx, BuildEvalMessage(taskSummary, diff), nil)
	if err != nil {
		return Verdict{}, fmt.Errorf("running evaluator: %w", err)
	}

	var sb strings.Builder
	for ev := range events {
		if ev.Type == runtime.EventTextDelta {
			sb.WriteString(ev.Text)
		}
		if ev.Type == runtime.EventError && ev.Error != nil {
			return Verdict{}, fmt.Errorf("evaluator event error: %w", ev.Error)
		}
	}

	return ParseVerdict(sb.String())
}

// sessionSuffix derives a short stable-ish suffix for the session id.
func sessionSuffix(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ReplaceAll(s, " ", "-")
}
