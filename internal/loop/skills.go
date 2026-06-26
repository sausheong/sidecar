package loop

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/tool/skills/disk"
	"github.com/sausheong/sidecar/internal/config"
)

// BuildSkillsProvider returns a harness SkillProvider backed by the target
// repo's skills directory (config.SkillsDir, default ".sidecar/skills"),
// or nil when that directory does not exist. A nil provider means the
// coding runtime gets no skills index and no load_skill tool — identical
// to the prior behavior, so repos without skills are unaffected.
func BuildSkillsProvider(repoPath string, cfg *config.Config) runtime.SkillProvider {
	dir := filepath.Join(repoPath, cfg.SkillsDir())
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	return disk.NewStore(dir).AsSkillProvider()
}

// buildSkillsProvider is the unexported call site used by Loop.New.
func buildSkillsProvider(repoPath string, cfg *config.Config) runtime.SkillProvider {
	p := BuildSkillsProvider(repoPath, cfg)
	if p != nil {
		slog.Info("sidecar: skills enabled", "dir", cfg.SkillsDir())
	}
	return p
}
