package cli

import (
	"log"
	"os"

	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/memory"
)

// buildEmbeddingProvider constructs an EmbeddingProvider from the embedding config.
// Returns nil if embedding is not configured — memory is then disabled silently.
func buildEmbeddingProvider(cfg *config.Config) memory.EmbeddingProvider {
	switch cfg.Embedding.Provider {
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			log.Printf("warning: embedding.provider=openai but OPENAI_API_KEY not set; memory disabled")
			return nil
		}
		return memory.NewOpenAI(apiKey, cfg.Embedding.Model)
	case "voyage":
		apiKey := os.Getenv("VOYAGE_API_KEY")
		if apiKey == "" {
			log.Printf("warning: embedding.provider=voyage but VOYAGE_API_KEY not set; memory disabled")
			return nil
		}
		return memory.NewVoyage(apiKey, cfg.Embedding.Model)
	default:
		return nil // no embedding configured — memory disabled
	}
}
