package loop

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestBuildReviewerPrompt_IncludesAllFields(t *testing.T) {
	task := store.Task{
		ID:         uuid.New(),
		SignalType: "ci_failure",
		Summary:    "fix CI failure in build @ abc12345",
		Status:     "completed",
	}
	events := []*store.TaskEvent{
		{Type: "triage", Payload: map[string]any{"change_type": "test_fix"}},
		{Type: "pr_created", Payload: map[string]any{"url": "https://x"}},
		{Type: "agent_step", Payload: map[string]any{"ignored": true}}, // not in whitelist
	}

	out := buildReviewerPrompt(task, events)

	assert.Contains(t, out, "memory.save")
	assert.Contains(t, out, "ci_failure")
	assert.Contains(t, out, "fix CI failure in build @ abc12345")
	assert.Contains(t, out, "completed")
	assert.Contains(t, out, "[triage]")
	assert.Contains(t, out, "[pr_created]")
	assert.Contains(t, out, "test_fix")
	assert.Contains(t, out, "https://x")
	assert.False(t, strings.Contains(out, "agent_step"), "non-whitelisted event types should be filtered out")
}

func TestBuildReviewerPrompt_NoEvents(t *testing.T) {
	task := store.Task{
		ID:         uuid.New(),
		SignalType: "schedule_tick",
		Summary:    "proactive sweep",
		Status:     "completed",
	}
	out := buildReviewerPrompt(task, nil)

	assert.Contains(t, out, "schedule_tick")
	assert.NotContains(t, out, "Key events:")
}
