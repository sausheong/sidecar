package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Workspace     WorkspaceConfig      `yaml:"workspace"`
	Signals       []SignalConfig       `yaml:"signals"`
	Autonomy      AutonomyPolicy       `yaml:"autonomy"`
	Models        ModelConfig          `yaml:"models"`
	Scope         ScopeConfig          `yaml:"scope"`
	Embedding     EmbeddingConfig      `yaml:"embedding"`
	Notifications []NotificationConfig `yaml:"notifications"`
	Verification  VerificationConfig   `yaml:"verification"`
	Skills        SkillsConfig         `yaml:"skills"`
	Budget        BudgetConfig         `yaml:"budget"`
}

// VerificationConfig controls the adversarial evaluator gate.
type VerificationConfig struct {
	// Enabled gates auto-commit and pull-request changes behind the
	// evaluator. Pointer so an absent value defaults to true.
	Enabled *bool `yaml:"enabled"`
}

// VerificationEnabled reports whether the evaluator gate is on. Defaults to
// true when unset — a default-off gate would not close the Nodding Loop.
func (c *Config) VerificationEnabled() bool {
	if c.Verification.Enabled == nil {
		return true
	}
	return *c.Verification.Enabled
}

// SkillsConfig points the loop at a directory of SKILL.md files in the target repo.
type SkillsConfig struct {
	Dir string `yaml:"dir"`
}

// SkillsDir returns the configured skills directory (relative to the repo root),
// defaulting to ".sidecar/skills".
func (c *Config) SkillsDir() string {
	if c.Skills.Dir == "" {
		return ".sidecar/skills"
	}
	return c.Skills.Dir
}

// BudgetConfig caps autonomous spend.
type BudgetConfig struct {
	// DailyTokens is the per-workspace per-UTC-day token ceiling
	// (input+output). 0 means unlimited.
	DailyTokens int `yaml:"daily_tokens"`
}

// DailyTokenBudget returns the daily token ceiling; 0 means unlimited.
func (c *Config) DailyTokenBudget() int {
	return c.Budget.DailyTokens
}

type NotificationConfig struct {
	Provider string      `yaml:"provider"` // "slack" | "webhook" | "email"
	Webhook  string      `yaml:"webhook"`  // Slack: literal URL or $ENV_VAR
	URL      string      `yaml:"url"`      // generic webhook: literal URL or $ENV_VAR
	Email    EmailConfig `yaml:"email"`    // email (SMTP) settings
	On       []string    `yaml:"on"`       // events: skipped, suggested, completed, failed, notified
}

type EmailConfig struct {
	SMTPHost string   `yaml:"smtp_host"`
	SMTPPort int      `yaml:"smtp_port"` // 587 = STARTTLS (default), 465 = implicit TLS
	Username string   `yaml:"username"`  // literal or $ENV_VAR
	Password string   `yaml:"password"`  // literal or $ENV_VAR
	From     string   `yaml:"from"`
	To       []string `yaml:"to"`
}

// ResolveUsername returns the SMTP username, expanding $ENV_VAR references.
func (e EmailConfig) ResolveUsername() string {
	if len(e.Username) > 0 && e.Username[0] == '$' {
		return os.Getenv(e.Username[1:])
	}
	return e.Username
}

// ResolvePassword returns the SMTP password, expanding $ENV_VAR references.
func (e EmailConfig) ResolvePassword() string {
	if len(e.Password) > 0 && e.Password[0] == '$' {
		return os.Getenv(e.Password[1:])
	}
	return e.Password
}

// ResolveWebhook returns the Slack webhook URL, expanding $ENV_VAR references.
func (n NotificationConfig) ResolveWebhook() string {
	if len(n.Webhook) > 0 && n.Webhook[0] == '$' {
		return os.Getenv(n.Webhook[1:])
	}
	return n.Webhook
}

// ResolveURL returns the generic webhook URL, expanding $ENV_VAR references.
func (n NotificationConfig) ResolveURL() string {
	if len(n.URL) > 0 && n.URL[0] == '$' {
		return os.Getenv(n.URL[1:])
	}
	return n.URL
}

type EmbeddingConfig struct {
	Provider string `yaml:"provider"` // "openai" | "voyage"; empty = disabled
	Model    string `yaml:"model"`    // optional; uses provider default if empty
}

type LogsSignalConfig struct {
	Files     []LogFile     `yaml:"files"`
	Processes []LogProcess  `yaml:"processes"`
	Patterns  []LogPattern  `yaml:"patterns"`
	Rate      LogRateConfig `yaml:"rate"`
}

type LogFile    struct{ Path    string `yaml:"path"` }
type LogProcess struct{ Command string `yaml:"command"` }

type LogPattern struct {
	Match       string `yaml:"match"`
	QuietPeriod string `yaml:"quiet_period"`
}

type LogRateConfig struct {
	Window      string `yaml:"window"`
	Threshold   int    `yaml:"threshold"`
	QuietPeriod string `yaml:"quiet_period"`
}

type MetricsSignalConfig struct {
	Provider   string   `yaml:"provider"`    // "datadog" | "prometheus"
	Endpoint   string   `yaml:"endpoint"`    // Prometheus base URL, e.g. "http://localhost:9090"
	Tags       []string `yaml:"tags"`        // Datadog: filter monitors by tags
	AlertNames []string `yaml:"alert_names"` // optional allowlist; empty = all alerts
}

type UptimeSignalConfig struct {
	Endpoints []UptimeEndpoint `yaml:"endpoints"`
}

type UptimeEndpoint struct {
	URL          string             `yaml:"url"`
	Timeout      string             `yaml:"timeout"`       // e.g. "5s"; default 10s
	ExpectStatus int                `yaml:"expect_status"` // default 200
	ExpectMaxMs  int                `yaml:"expect_max_ms"` // latency threshold; 0 = disabled
	Diagnostics  []UptimeDiagnostic `yaml:"diagnostics"`   // empty = auto (dns, tcp, tls for https)
}

// UptimeDiagnostic defines one diagnostic check to run on failure.
// Built-in checks: dns, tcp, tls, ping, http, cross, shell.
type UptimeDiagnostic struct {
	Check   string `yaml:"check"`   // built-in check name or "shell" / "http"
	URL     string `yaml:"url"`     // for check: http — alternate URL to probe
	Command string `yaml:"command"` // for check: shell — command to run
}

type WorkspaceConfig struct {
	Name     string `yaml:"name"`
	Language string `yaml:"language"`
}

type SignalConfig struct {
	Adapter      string              `yaml:"adapter"`
	Watch        []string            `yaml:"watch"`
	Cron         string              `yaml:"cron"`
	Repo         string              `yaml:"repo"`          // owner/repo slug (github-ci adapter)
	Token        string              `yaml:"token"`         // literal or $ENV_VAR reference
	PollInterval string              `yaml:"poll_interval"` // e.g. "60s", default "60s"
	Logs         LogsSignalConfig    `yaml:"logs"`
	Metrics      MetricsSignalConfig `yaml:"metrics"`
	Uptime       UptimeSignalConfig  `yaml:"uptime"`
}

type AutonomyPolicy struct {
	DependencyUpdates string `yaml:"dependency_updates"`
	TestFixes         string `yaml:"test_fixes"`
	BugFixes          string `yaml:"bug_fixes"`
	Refactoring       string `yaml:"refactoring"`
	SchemaChanges     string `yaml:"schema_changes"`
	LogFixes          string `yaml:"log_fixes"`
	MetricFixes       string `yaml:"metric_fixes"`
	UptimeFixes       string `yaml:"uptime_fixes"`
}

type ModelConfig struct {
	Planning  string `yaml:"planning"`
	Coding    string `yaml:"coding"`
	Triage    string `yaml:"triage"`
	Evaluator string `yaml:"evaluator"`
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
	"notify":       true,
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
