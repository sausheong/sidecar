package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/sausheong/sidecar/internal/store"
)

func statusCmd() *cobra.Command {
	var repoFlag string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show running and recent sidecar tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbURL := os.Getenv("SIDECAR_DB_URL")
			if dbURL == "" {
				return fmt.Errorf("SIDECAR_DB_URL environment variable is required")
			}

			repoPath := repoFlag
			if repoPath == "" {
				repoPath = "."
			}
			abs, err := filepath.Abs(repoPath)
			if err != nil {
				return err
			}

			ctx := context.Background()
			db, err := store.Connect(ctx, dbURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()

			ws, err := db.GetWorkspaceByPath(ctx, abs)
			if err != nil {
				return fmt.Errorf("workspace not found at %s — run 'sidecar attach' first", abs)
			}

			tasks, err := db.ListTasks(ctx, ws.ID, 20)
			if err != nil {
				return fmt.Errorf("listing tasks: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "STATUS\tSIGNAL\tSUMMARY\tCREATED")
			for _, t := range tasks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					t.Status, t.SignalType, t.Summary,
					t.CreatedAt.Format("2006-01-02 15:04:05"))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path to the target repository (default: .)")
	return cmd
}
