package memory

import (
	"context"
	"strings"

	"github.com/sausheong/sidecar/internal/store"
)

const similarityThreshold = 0.7

// EmbeddingProvider converts text to vector embeddings.
type EmbeddingProvider interface {
	// Embed converts texts to float32 embedding vectors.
	// inputType is "document" (when storing) or "query" (when searching).
	// OpenAI ignores inputType; Voyage AI uses it for retrieval quality.
	Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error)
	// Dims returns the vector dimension this provider produces.
	Dims() int
}

// FormatMemoryBlock formats retrieved memory entries and policies as a markdown
// section for injection into the system prompt.
// Returns "" if no entries meet the similarity threshold and no policies exist.
func FormatMemoryBlock(results []*store.MemorySearchResult, policies []string) string {
	var semantic, procedural []string
	for _, r := range results {
		if r.Similarity < similarityThreshold {
			continue
		}
		switch r.Category {
		case "semantic":
			semantic = append(semantic, r.Content)
		case "procedural":
			procedural = append(procedural, r.Content)
		}
	}

	if len(semantic) == 0 && len(procedural) == 0 && len(policies) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Workspace Memory\n")

	if len(semantic) > 0 {
		sb.WriteString("\n**Architecture:**\n")
		for _, s := range semantic {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(procedural) > 0 {
		sb.WriteString("\n**Workflows:**\n")
		for _, s := range procedural {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(policies) > 0 {
		sb.WriteString("\n**Policies:**\n")
		for _, p := range policies {
			sb.WriteString("- " + p + "\n")
		}
	}
	return sb.String()
}
