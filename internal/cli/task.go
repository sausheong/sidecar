package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/sausheong/sidecar/internal/store"
)

func taskCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "task <description>",
		Short: "Submit an on-demand improvement task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			description := args[0]

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

			if err := store.Migrate(ctx, db); err != nil {
				return err
			}

			cfg, err := config.Load(filepath.Join(abs, "sidecar.yaml"))
			if err != nil {
				log.Printf("warning: no sidecar.yaml at %s, using defaults", abs)
				cfg = &config.Config{}
			}

			ws, err := db.GetWorkspaceByPath(ctx, abs)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("looking up workspace: %w", err)
				}
				// workspace doesn't exist yet — create it
				ws = &store.Workspace{
					Name:       filepath.Base(abs),
					Path:       abs,
					ConfigHash: "",
				}
				if err := db.UpsertWorkspace(ctx, ws); err != nil {
					return err
				}
			}

			sig := adapter.Signal{
				Type:   adapter.SignalOnDemand,
				Source: "cli",
				Payload: map[string]any{
					"description": description,
				},
			}

			l := loop.New(db, ws, cfg, abs, buildEmbeddingProvider(cfg))
			log.Printf("Running task: %s", description)
			return l.Run(ctx, sig)
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path to the target repository (default: .)")
	return cmd
}
