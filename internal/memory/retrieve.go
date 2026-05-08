package memory

import (
	"context"
	"fmt"

	"github.com/sausheong/sidecar/internal/store"
)

const defaultRetrieveLimit = 5

// Retrieve fetches relevant memory entries for the current signal query and formats
// them as a markdown block for injection into the system prompt.
// Returns "" if provider is nil, memory is empty, or no entries are relevant.
func Retrieve(ctx context.Context, provider EmbeddingProvider, db *store.DB, workspace *store.Workspace, query string, limit int) (string, error) {
	if provider == nil {
		return "", nil
	}
	if limit <= 0 {
		limit = defaultRetrieveLimit
	}

	// Embed the query using "query" input type for retrieval-optimized vectors.
	embeddings, err := provider.Embed(ctx, []string{query}, "query")
	if err != nil {
		return "", fmt.Errorf("embedding retrieval query: %w", err)
	}
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return "", nil
	}

	results, err := db.SearchMemory(ctx, workspace.ID, []string{"semantic", "procedural"}, embeddings[0], limit)
	if err != nil {
		return "", fmt.Errorf("searching memory: %w", err)
	}

	policies, err := db.GetPolicies(ctx, workspace.ID)
	if err != nil {
		return "", fmt.Errorf("loading policies: %w", err)
	}

	return FormatMemoryBlock(results, policies), nil
}
