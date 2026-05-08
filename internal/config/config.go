package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Workspace WorkspaceConfig `yaml:"workspace"`
	Signals   []SignalConfig  `yaml:"signals"`
	Autonomy  AutonomyPolicy  `yaml:"autonomy"`
	Models    ModelConfig     `yaml:"models"`
	Scope     ScopeConfig     `yaml:"scope"`
}

type WorkspaceConfig struct {
	Name     string `yaml:"name"`
	Language string `yaml:"language"`
}

type SignalConfig struct {
	Adapter      string   `yaml:"adapter"`
	Watch        []string `yaml:"watch"`
	Cron         string   `yaml:"cron"`
	Repo         string   `yaml:"repo"`          // owner/repo slug (github-ci adapter)
	Token        string   `yaml:"token"`         // literal or $ENV_VAR reference
	PollInterval string   `yaml:"poll_interval"` // e.g. "60s", default "60s"
}

type AutonomyPolicy struct {
	DependencyUpdates string `yaml:"dependency_updates"`
	TestFixes         string `yaml:"test_fixes"`
	BugFixes          string `yaml:"bug_fixes"`
	Refactoring       string `yaml:"refactoring"`
	SchemaChanges     string `yaml:"schema_changes"`
}

type ModelConfig struct {
	Planning string `yaml:"planning"`
	Coding   string `yaml:"coding"`
	Triage   string `yaml:"triage"`
}

type ScopeConfig struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

// ParsedPollInterval returns the poll interval as a time.Duration.
// Returns 60 seconds if PollInterval is empty or unparseable.
func (s SignalConfig) ParsedPollInterval() time.Duration {
	if s.PollInterval == "" {
		return 60 * time.Second
	}
	d, err := time.ParseDuration(s.PollInterval)
	if err != nil || d <= 0 {
		return 60 * time.Second
	}
	return d
}

// ResolveToken returns the resolved token value.
// If Token starts with "$", it is treated as an environment variable name
// and resolved via os.Getenv. Otherwise the literal value is returned.
func (s SignalConfig) ResolveToken() string {
	if len(s.Token) > 0 && s.Token[0] == '$' {
		return os.Getenv(s.Token[1:])
	}
	return s.Token
}

var validAutonomyLevels = map[string]bool{
	"auto-commit":  true,
	"pull-request": true,
	"suggest-only": true,
}

func ValidAutonomyLevel(s string) bool {
	return validAutonomyLevels[s]
}

// Load reads and parses the YAML config at path. It is intentionally permissive:
// it does not validate field values. Callers that need to validate autonomy levels
// must call ValidAutonomyLevel separately.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}
	return &cfg, nil
}
