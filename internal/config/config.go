package config

import (
	"fmt"
	"os"

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
	Adapter string   `yaml:"adapter"`
	Watch   []string `yaml:"watch"`
	Cron    string   `yaml:"cron"`
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

var validAutonomyLevels = map[string]bool{
	"auto-commit":  true,
	"pull-request": true,
	"suggest-only": true,
}

func ValidAutonomyLevel(s string) bool {
	return validAutonomyLevels[s]
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading sidecar.yaml: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing sidecar.yaml: %w", err)
	}
	return &cfg, nil
}
