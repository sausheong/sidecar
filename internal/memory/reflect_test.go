package memory_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReflectResponse_Valid(t *testing.T) {
	raw := `{"episodic":"Fixed failing auth tests.","semantic":["auth uses interface mocking"],"procedural":["run make test-unit"],"policies":[]}`
	resp, err := memory.ParseReflectResponse(raw)
	require.NoError(t, err)
	assert.Equal(t, "Fixed failing auth tests.", resp.Episodic)
	assert.Equal(t, []string{"auth uses interface mocking"}, resp.Semantic)
	assert.Equal(t, []string{"run make test-unit"}, resp.Procedural)
	assert.Empty(t, resp.Policies)
}

func TestParseReflectResponse_Invalid(t *testing.T) {
	_, err := memory.ParseReflectResponse("not json")
	assert.Error(t, err)
}

func TestBuildReflectMessage(t *testing.T) {
	task := &store.Task{
		SignalType: "ci.failure",
		Summary:   "fix CI failure in CI @ abc123",
		Status:    "completed",
	}
	events := []*store.TaskEvent{
		{
			ID:      uuid.New(),
			TaskID:  task.ID,
			Type:    "triage",
			Payload: map[string]any{"should_act": true, "change_type": "test_fix"},
		},
	}
	msg := memory.BuildReflectMessage(task, events)
	assert.Contains(t, msg, "ci.failure")
	assert.Contains(t, msg, "fix CI failure")
	assert.Contains(t, msg, "triage")
	assert.Contains(t, msg, "test_fix")
}
