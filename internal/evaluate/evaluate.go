// Package evaluate implements the generator/evaluator split: a fresh skeptic
// runtime reviews the generator's diff and can REJECT it before any commit.
// This closes the "Nodding Loop" — an agent grading its own work.
package evaluate

import (
	"encoding/json"
	"fmt"
	"strings"
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
