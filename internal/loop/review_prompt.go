package loop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sausheong/sidecar/internal/store"
)

const reviewerInstructions = `You are reviewing a just-completed engineering task.
Extract durable knowledge that will help the next time a similar signal arrives.

Use the memory.save tool. Pass tags to categorize each save:
  ["episodic"]   — what was done and the outcome (one save per task, always)
  ["semantic"]   — architectural insights, code conventions, fragile areas
  ["procedural"] — specific commands or workflows that worked

Guidelines:
- Always emit one episodic save summarizing the task.
- Emit semantic/procedural saves only when you learned something not
  derivable from reading the codebase fresh.
- One sentence per save. Keep content concise.
- Do not save anything if the task produced no useful insight.`

// reviewerEventTypes is the set of task_event types worth surfacing
// in the reviewer prompt. Other event types (per-step traces, etc.)
// are noise for memory extraction.
var reviewerEventTypes = map[string]bool{
	"triage":     true,
	"pr_created": true,
	"suggestion": true,
}

// buildReviewerPrompt composes the per-task review instruction message
// the reviewer receives. It includes the task's signal/summary/status
// and a filtered slice of events.
func buildReviewerPrompt(task store.Task, events []*store.TaskEvent) string {
	var sb strings.Builder
	sb.WriteString(reviewerInstructions)
	sb.WriteString("\n\nTask context:\n")
	fmt.Fprintf(&sb, "  Signal: %s\n", task.SignalType)
	fmt.Fprintf(&sb, "  Summary: %s\n", task.Summary)
	fmt.Fprintf(&sb, "  Status: %s\n", task.Status)

	var kept []*store.TaskEvent
	for _, ev := range events {
		if reviewerEventTypes[ev.Type] {
			kept = append(kept, ev)
		}
	}
	if len(kept) > 0 {
		sb.WriteString("\nKey events:\n")
		for _, ev := range kept {
			data, _ := json.Marshal(ev.Payload)
			fmt.Fprintf(&sb, "  [%s] %s\n", ev.Type, string(data))
		}
	}
	return sb.String()
}
