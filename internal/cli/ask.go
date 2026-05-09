package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/sausheong/harness/providers/anthropic"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/sausheong/sidecar/internal/memory"
	"github.com/sausheong/sidecar/internal/store"
)

func askCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask a question about the codebase using workspace memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			question := args[0]

			repoPath := repoFlag
			if repoPath == "" {
				repoPath = "."
			}
			abs, err := filepath.Abs(repoPath)
			if err != nil {
				return err
			}

			dbURL := os.Getenv("SIDECAR_DB_URL")
			if dbURL == "" {
				return fmt.Errorf("SIDECAR_DB_URL environment variable is required")
			}

			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				return fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
			}

			ctx := context.Background()
			db, err := store.Connect(ctx, dbURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()

			cfg, err := config.Load(filepath.Join(abs, "sidecar.yaml"))
			if err != nil {
				cfg = &config.Config{}
			}

			embeddingProvider := buildEmbeddingProvider(cfg)
			if embeddingProvider == nil {
				return fmt.Errorf("embedding not configured; add an 'embedding' section to sidecar.yaml")
			}

			ws, err := db.GetWorkspaceByPath(ctx, abs)
			if err != nil {
				return fmt.Errorf("workspace not found at %s — run 'sidecar attach' first", abs)
			}

			models := loop.ResolveModels(cfg)
			llmProvider := anthropic.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), "")

			answer, err := memory.Ask(ctx, embeddingProvider, llmProvider, models.Triage, db, ws, question)
			if err != nil {
				return fmt.Errorf("asking: %w", err)
			}

			fmt.Println(answer)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path to the target repository (default: .)")
	return cmd
}
