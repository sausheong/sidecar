package memory_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestFormatMemoryBlock_WithResults(t *testing.T) {
	results := []*store.MemorySearchResult{
		{Category: "semantic", Content: "auth uses interface mocking", Similarity: 0.92},
		{Category: "procedural", Content: "run 'make test-unit' for fast tests", Similarity: 0.85},
	}
	policies := []string{"prefer explicit error handling"}

	block := memory.FormatMemoryBlock(results, policies)
	assert.Contains(t, block, "## Workspace Memory")
	assert.Contains(t, block, "auth uses interface mocking")
	assert.Contains(t, block, "make test-unit")
	assert.Contains(t, block, "prefer explicit error handling")
	assert.Contains(t, block, "**Architecture:**")
	assert.Contains(t, block, "**Workflows:**")
	assert.Contains(t, block, "**Policies:**")
}

func TestFormatMemoryBlock_BelowThreshold(t *testing.T) {
	results := []*store.MemorySearchResult{
		{Category: "semantic", Content: "low similarity entry", Similarity: 0.5},
	}
	block := memory.FormatMemoryBlock(results, nil)
	assert.Equal(t, "", block, "entries below 0.7 threshold should be excluded")
}

func TestFormatMemoryBlock_Empty(t *testing.T) {
	block := memory.FormatMemoryBlock(nil, nil)
	assert.Equal(t, "", block, "no entries and no policies → empty string")
}

func TestFormatMemoryBlock_PoliciesOnly(t *testing.T) {
	block := memory.FormatMemoryBlock(nil, []string{"never touch secrets/"})
	assert.Contains(t, block, "## Workspace Memory")
	assert.Contains(t, block, "never touch secrets/")
	assert.NotContains(t, block, "**Architecture:**")
}
