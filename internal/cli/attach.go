package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/sausheong/sidecar/internal/adapter"
	gitadapter "github.com/sausheong/sidecar/internal/adapter/git"
	"github.com/sausheong/sidecar/internal/adapter/githubci"
	logsadapter "github.com/sausheong/sidecar/internal/adapter/logs"
	"github.com/sausheong/sidecar/internal/adapter/schedule"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/daemon"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/sausheong/sidecar/internal/store"
)

func attachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [path]",
		Short: "Attach to a project and start the sidecar daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := "."
			if len(args) > 0 {
				repoPath = args[0]
			}
			abs, err := filepath.Abs(repoPath)
			if err != nil {
				return err
			}

			cfgPath := filepath.Join(abs, "sidecar.yaml")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("sidecar.yaml not found or invalid in %s: %w", abs, err)
			}

			dbURL := os.Getenv("SIDECAR_DB_URL")
			if dbURL == "" {
				return fmt.Errorf("SIDECAR_DB_URL environment variable is required")
			}

			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				return fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			db, err := store.Connect(ctx, dbURL)
			if err != nil {
				return fmt.Errorf("connecting to database: %w", err)
			}
			defer db.Close()

			if err := store.Migrate(ctx, db); err != nil {
				return fmt.Errorf("running migrations: %w", err)
			}

			configData, err := os.ReadFile(cfgPath)
			if err != nil {
				return fmt.Errorf("reading config for hash: %w", err)
			}
			hash := sha256.Sum256(configData)
			configHash := fmt.Sprintf("%x", hash[:8])

			ws := &store.Workspace{
				Name:       cfg.Workspace.Name,
				Path:       abs,
				ConfigHash: configHash,
			}
			if err := db.UpsertWorkspace(ctx, ws); err != nil {
				return fmt.Errorf("upserting workspace: %w", err)
			}

			adapters := buildAdapters(abs, cfg)
			l := loop.New(db, ws, cfg, abs, buildEmbeddingProvider(cfg))
			d := daemon.New(adapters, l.Run)

			if err := d.Start(ctx); err != nil {
				return fmt.Errorf("starting daemon: %w", err)
			}

			log.Printf("Sidecar attached to %s — watching %d adapter(s)", abs, len(adapters))

			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			<-quit

			log.Println("Shutting down...")
			d.Stop()
			return nil
		},
	}
}

func buildAdapters(repoPath string, cfg *config.Config) []adapter.Adapter {
	var adapters []adapter.Adapter
	for _, sig := range cfg.Signals {
		switch sig.Adapter {
		case "git":
			adapters = append(adapters, gitadapter.New(repoPath))
		case "schedule":
			if a, err := schedule.New(sig.Cron); err == nil {
				adapters = append(adapters, a)
			} else {
				log.Printf("invalid cron %q: %v", sig.Cron, err)
			}
		case "github-ci":
			token := sig.ResolveToken()
			interval := sig.ParsedPollInterval()
			adapters = append(adapters, githubci.New(sig.Repo, token, interval, sig.Watch))
		case "logs":
			adapters = append(adapters, logsadapter.New(sig))
		default:
			log.Printf("unknown adapter type %q, skipping", sig.Adapter)
		}
	}
	return adapters
}
