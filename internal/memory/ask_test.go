package memory_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestBuildAskMessage_WithMemory(t *testing.T) {
	entries := []*store.MemorySearchResult{
		{Category: "semantic", Content: "auth uses interface mocking", Similarity: 0.92},
		{Category: "procedural", Content: "run make test-unit for fast tests", Similarity: 0.85},
		{Category: "episodic", Content: "Fixed auth bug on 2026-05-01. Root cause: stale mock.", Similarity: 0.80},
	}
	policies := []string{"prefer explicit error handling"}
	msg := memory.BuildAskMessage(entries, policies, "how does auth work?")

	assert.Contains(t, msg, "auth uses interface mocking")
	assert.Contains(t, msg, "make test-unit")
	assert.Contains(t, msg, "Fixed auth bug")
	assert.Contains(t, msg, "prefer explicit error handling")
	assert.Contains(t, msg, "how does auth work?")
}

func TestBuildAskMessage_Empty(t *testing.T) {
	msg := memory.BuildAskMessage(nil, nil, "how does auth work?")
	assert.Contains(t, msg, "how does auth work?")
	assert.Contains(t, msg, "no memory") // should indicate empty
}
