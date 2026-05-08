# Sidecar Phase 1 — Core Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Sidecar daemon with git + schedule adapters, Harness-backed improvement loop, PostgreSQL task store, and `attach`/`task`/`status` CLI commands.

**Architecture:** A cobra CLI starts the daemon via `sidecar attach`. The daemon reads `sidecar.yaml`, starts adapters (git poller + cron scheduler), and routes each signal through the improvement loop — a Harness `runtime.Runtime` that reads/writes files and runs bash commands, then commits any changes to a `sidecar/<task-id>` branch. All task state persists in PostgreSQL.

**Tech Stack:** Go 1.25+, `github.com/sausheong/harness` (local replace), `github.com/spf13/cobra v1.9`, `github.com/jackc/pgx/v5 v5.7`, `github.com/robfig/cron/v3 v3.0`, `gopkg.in/yaml.v3 v3.0`, `github.com/stretchr/testify v1.10`

---

## File Map

```
github.com/sausheong/sidecar          (directory: /Users/sausheong/projects/forge)
├── cmd/sidecar/main.go               CLI entrypoint
├── internal/
│   ├── cli/
│   │   ├── root.go                   cobra root command + global flags
│   │   ├── attach.go                 `sidecar attach` — starts daemon
│   │   ├── task.go                   `sidecar task` — submit on-demand task
│   │   └── status.go                 `sidecar status` — list recent tasks
│   ├── config/
│   │   ├── config.go                 sidecar.yaml → Config struct
│   │   └── config_test.go
│   ├── store/
│   │   ├── db.go                     pgx connection + embedded schema migration
│   │   ├── workspace.go              workspaces CRUD
│   │   ├── task.go                   tasks + task_events CRUD
│   │   └── store_test.go             integration tests (build tag: integration)
│   ├── adapter/
│   │   ├── adapter.go                Adapter interface + Signal/SignalType
│   │   ├── git/
│   │   │   ├── git.go                polls git log for new commits
│   │   │   └── git_test.go           uses a tmp git repo
│   │   └── schedule/
│   │       ├── schedule.go           wraps robfig/cron
│   │       └── schedule_test.go
│   ├── loop/
│   │   ├── loop.go                   improvement loop (wraps Harness runtime)
│   │   └── loop_test.go
│   ├── output/
│   │   ├── output.go                 git commit to sidecar/<task-id> branch
│   │   └── output_test.go
│   └── daemon/
│       └── daemon.go                 starts adapters, routes signals to loop
├── go.mod
├── go.sum
└── sidecar.yaml                      example config for reference
```

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `SIDECAR_DB_URL` | yes | PostgreSQL DSN, e.g. `postgres://user:pass@localhost:5432/sidecar` |
| `ANTHROPIC_API_KEY` | yes | Anthropic API key for Harness runtime |

---

### Task 1: Project Setup

**Files:**
- Create: `go.mod`
- Create: `cmd/sidecar/main.go`

- [ ] **Step 1: Initialize the module**

```bash
cd /Users/sausheong/projects/forge
go mod init github.com/sausheong/sidecar
```

Expected: `go.mod` created with `module github.com/sausheong/sidecar` and `go 1.25`

- [ ] **Step 2: Add the replace directive and dependencies to go.mod**

Edit `go.mod` to:

```
module github.com/sausheong/sidecar

go 1.25

require (
	github.com/jackc/pgx/v5 v5.7.5
	github.com/robfig/cron/v3 v3.0.1
	github.com/sausheong/harness v0.0.0
	github.com/spf13/cobra v1.9.1
	github.com/stretchr/testify v1.10.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/sausheong/harness => ../harness
```

- [ ] **Step 3: Create the CLI entrypoint**

Create `cmd/sidecar/main.go`:

```go
package main

import (
	"os"

	"github.com/sausheong/sidecar/internal/cli"
)

func main() {
	if err := cli.RootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Create the root command stub**

Create `internal/cli/root.go`:

```go
package cli

import "github.com/spf13/cobra"

func RootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sidecar",
		Short: "Autonomous engineering agent that continuously maintains your software",
	}
	root.AddCommand(attachCmd(), taskCmd(), statusCmd())
	return root
}
```

Create stub files so it compiles. Create `internal/cli/attach.go`:

```go
package cli

import "github.com/spf13/cobra"

func attachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [path]",
		Short: "Attach to a project and start the sidecar daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil // implemented in Task 11
		},
	}
}
```

Create `internal/cli/task.go`:

```go
package cli

import "github.com/spf13/cobra"

func taskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "task <description>",
		Short: "Submit an on-demand task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil // implemented in Task 12
		},
	}
}
```

Create `internal/cli/status.go`:

```go
package cli

import "github.com/spf13/cobra"

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show recent tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil // implemented in Task 13
		},
	}
}
```

- [ ] **Step 5: Fetch dependencies and verify it builds**

```bash
go mod tidy
go build ./...
```

Expected: no errors, binary produced.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd/ internal/cli/
git commit -m "feat: project skeleton with cobra CLI stubs"
```

---

### Task 2: Config Parsing

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `sidecar.yaml` (example)

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	yaml := `
workspace:
  name: my-service
  language: go
signals:
  - adapter: git
    watch: [push, pr]
  - adapter: schedule
    cron: "0 2 * * *"
autonomy:
  dependency_updates: auto-commit
  test_fixes: auto-commit
  bug_fixes: pull-request
  refactoring: suggest-only
  schema_changes: suggest-only
models:
  planning: anthropic/claude-sonnet-4-6
  coding: anthropic/claude-sonnet-4-6
  triage: anthropic/claude-haiku-4-5
scope:
  include: [src/, tests/]
  exclude: [secrets/]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0644))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	assert.Equal(t, "my-service", cfg.Workspace.Name)
	assert.Equal(t, "go", cfg.Workspace.Language)
	assert.Len(t, cfg.Signals, 2)
	assert.Equal(t, "git", cfg.Signals[0].Adapter)
	assert.Equal(t, []string{"push", "pr"}, cfg.Signals[0].Watch)
	assert.Equal(t, "schedule", cfg.Signals[1].Adapter)
	assert.Equal(t, "0 2 * * *", cfg.Signals[1].Cron)
	assert.Equal(t, "auto-commit", cfg.Autonomy.DependencyUpdates)
	assert.Equal(t, "pull-request", cfg.Autonomy.BugFixes)
	assert.Equal(t, "anthropic/claude-haiku-4-5", cfg.Models.Triage)
	assert.Equal(t, []string{"src/", "tests/"}, cfg.Scope.Include)
	assert.Equal(t, []string{"secrets/"}, cfg.Scope.Exclude)
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := config.Load("/does/not/exist/sidecar.yaml")
	assert.Error(t, err)
}

func TestAutonomyLevel_Valid(t *testing.T) {
	cases := []string{"auto-commit", "pull-request", "suggest-only"}
	for _, c := range cases {
		assert.True(t, config.ValidAutonomyLevel(c), "expected %q to be valid", c)
	}
	assert.False(t, config.ValidAutonomyLevel("invalid"))
}
```

- [ ] **Step 2: Run the test to confirm it fails**

```bash
go test ./internal/config/...
```

Expected: FAIL — `config` package does not exist yet.

- [ ] **Step 3: Implement config.go**

Create `internal/config/config.go`:

```go
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
```

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
go test ./internal/config/... -v
```

Expected: PASS — all three test functions pass.

- [ ] **Step 5: Write the example sidecar.yaml**

Create `sidecar.yaml` in the project root:

```yaml
workspace:
  name: my-service
  language: go

signals:
  - adapter: git
    watch: [push, pr]
  - adapter: schedule
    cron: "0 2 * * *"

autonomy:
  dependency_updates: auto-commit
  test_fixes: auto-commit
  bug_fixes: pull-request
  refactoring: suggest-only
  schema_changes: suggest-only

models:
  planning: anthropic/claude-sonnet-4-6
  coding: anthropic/claude-sonnet-4-6
  triage: anthropic/claude-haiku-4-5

scope:
  include: [src/, tests/]
  exclude: [secrets/]
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/ sidecar.yaml
git commit -m "feat: sidecar.yaml config parsing"
```

---

### Task 3: PostgreSQL Schema + DB Connection

**Files:**
- Create: `internal/store/db.go`
- Create: `internal/store/schema.sql` (embedded)

- [ ] **Step 1: Write the failing test**

Create `internal/store/store_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dbURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SIDECAR_TEST_DB_URL")
	if url == "" {
		t.Skip("SIDECAR_TEST_DB_URL not set")
	}
	return url
}

func TestConnect(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	assert.NotNil(t, db)
}

func TestMigrate(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()

	err = store.Migrate(context.Background(), db)
	require.NoError(t, err)

	// Running migrate twice is idempotent
	err = store.Migrate(context.Background(), db)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run to confirm it fails**

```bash
go test -tags integration ./internal/store/... -v
```

Expected: FAIL — `store` package does not exist.

- [ ] **Step 3: Create the embedded schema**

Create `internal/store/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS workspaces (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    path        TEXT NOT NULL UNIQUE,
    config_hash TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tasks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    signal_type  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    summary      TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS task_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    UUID NOT NULL REFERENCES tasks(id),
    type       TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 4: Implement db.go**

Create `internal/store/db.go`:

```go
package store

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type DB struct {
	pool *pgxpool.Pool
}

func Connect(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (db *DB) Close() {
	db.pool.Close()
}

func Migrate(ctx context.Context, db *DB) error {
	_, err := db.pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run the integration test**

```bash
# Requires a running PostgreSQL with a sidecar database
createdb sidecar 2>/dev/null || true
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v
```

Expected: PASS — TestConnect and TestMigrate both pass.

- [ ] **Step 6: Commit**

```bash
git add internal/store/db.go internal/store/schema.sql internal/store/store_test.go
git commit -m "feat: PostgreSQL connection and schema migration"
```

---

### Task 4: Workspace and Task Store

**Files:**
- Create: `internal/store/workspace.go`
- Create: `internal/store/task.go`
- Modify: `internal/store/store_test.go`

- [ ] **Step 1: Write failing tests — add to store_test.go**

Append to `internal/store/store_test.go`:

```go
func TestWorkspace_UpsertAndGet(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{
		Name:       "test-service",
		Path:       t.TempDir(),
		ConfigHash: "abc123",
	}
	err = db.UpsertWorkspace(context.Background(), ws)
	require.NoError(t, err)
	assert.NotEmpty(t, ws.ID)

	got, err := db.GetWorkspaceByPath(context.Background(), ws.Path)
	require.NoError(t, err)
	assert.Equal(t, "test-service", got.Name)
	assert.Equal(t, "abc123", got.ConfigHash)

	// Upsert again with updated hash — idempotent
	ws.ConfigHash = "def456"
	err = db.UpsertWorkspace(context.Background(), ws)
	require.NoError(t, err)

	got, err = db.GetWorkspaceByPath(context.Background(), ws.Path)
	require.NoError(t, err)
	assert.Equal(t, "def456", got.ConfigHash)
}

func TestTask_CreateAndList(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "svc", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	task := &store.Task{
		WorkspaceID: ws.ID,
		SignalType:  "git.commit",
		Summary:     "fix: patched auth handler",
	}
	err = db.CreateTask(context.Background(), task)
	require.NoError(t, err)
	assert.NotEmpty(t, task.ID)
	assert.Equal(t, "pending", task.Status)

	tasks, err := db.ListTasks(context.Background(), ws.ID, 10)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "git.commit", tasks[0].SignalType)

	err = db.UpdateTaskStatus(context.Background(), task.ID, "completed")
	require.NoError(t, err)

	tasks, err = db.ListTasks(context.Background(), ws.ID, 10)
	require.NoError(t, err)
	assert.Equal(t, "completed", tasks[0].Status)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v
```

Expected: FAIL — `UpsertWorkspace`, `Workspace`, `Task` types not defined.

- [ ] **Step 3: Implement workspace.go**

Create `internal/store/workspace.go`:

```go
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type Workspace struct {
	ID         uuid.UUID
	Name       string
	Path       string
	ConfigHash string
}

func (db *DB) UpsertWorkspace(ctx context.Context, ws *Workspace) error {
	row := db.pool.QueryRow(ctx, `
		INSERT INTO workspaces (name, path, config_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (path) DO UPDATE
		  SET name = EXCLUDED.name,
		      config_hash = EXCLUDED.config_hash
		RETURNING id`,
		ws.Name, ws.Path, ws.ConfigHash,
	)
	return row.Scan(&ws.ID)
}

func (db *DB) GetWorkspaceByPath(ctx context.Context, path string) (*Workspace, error) {
	ws := &Workspace{}
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, path, config_hash FROM workspaces WHERE path = $1`, path,
	).Scan(&ws.ID, &ws.Name, &ws.Path, &ws.ConfigHash)
	if err != nil {
		return nil, fmt.Errorf("getting workspace: %w", err)
	}
	return ws, nil
}
```

- [ ] **Step 4: Implement task.go**

Create `internal/store/task.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Task struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	SignalType  string
	Status      string
	Summary     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (db *DB) CreateTask(ctx context.Context, t *Task) error {
	if t.Status == "" {
		t.Status = "pending"
	}
	row := db.pool.QueryRow(ctx, `
		INSERT INTO tasks (workspace_id, signal_type, status, summary)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		t.WorkspaceID, t.SignalType, t.Status, t.Summary,
	)
	return row.Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

func (db *DB) UpdateTaskStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE tasks SET status = $1, updated_at = NOW() WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("updating task status: %w", err)
	}
	return nil
}

func (db *DB) ListTasks(ctx context.Context, workspaceID uuid.UUID, limit int) ([]*Task, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, workspace_id, signal_type, status, summary, created_at, updated_at
		FROM tasks
		WHERE workspace_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.SignalType, &t.Status,
			&t.Summary, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
```

- [ ] **Step 5: Add uuid dependency and tidy**

```bash
go get github.com/google/uuid
go mod tidy
```

- [ ] **Step 6: Run the tests**

```bash
SIDECAR_TEST_DB_URL="postgres://localhost/sidecar" go test -tags integration ./internal/store/... -v
```

Expected: PASS — all four tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/store/workspace.go internal/store/task.go internal/store/store_test.go go.mod go.sum
git commit -m "feat: workspace and task store"
```

---

### Task 5: Adapter Interface

**Files:**
- Create: `internal/adapter/adapter.go`

- [ ] **Step 1: Write the test**

Create `internal/adapter/adapter_test.go`:

```go
package adapter_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/stretchr/testify/assert"
)

func TestSignalTypes(t *testing.T) {
	assert.Equal(t, adapter.SignalType("git.commit"), adapter.SignalGitCommit)
	assert.Equal(t, adapter.SignalType("schedule.tick"), adapter.SignalScheduleTick)
	assert.Equal(t, adapter.SignalType("ondemand.task"), adapter.SignalOnDemand)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Implement adapter.go**

Create `internal/adapter/adapter.go`:

```go
package adapter

import "context"

type SignalType string

const (
	SignalGitCommit   SignalType = "git.commit"
	SignalScheduleTick SignalType = "schedule.tick"
	SignalOnDemand    SignalType = "ondemand.task"
)

type Signal struct {
	Type    SignalType
	Source  string
	Payload map[string]any
}

type Adapter interface {
	Name() string
	Start(ctx context.Context, out chan<- Signal) error
	Stop() error
}
```

- [ ] **Step 4: Run the test**

```bash
go test ./internal/adapter/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/
git commit -m "feat: adapter interface and signal types"
```

---

### Task 6: Git Adapter

**Files:**
- Create: `internal/adapter/git/git.go`
- Create: `internal/adapter/git/git_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/git/git_test.go`:

```go
package git_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	gitadapter "github.com/sausheong/sidecar/internal/adapter/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}

func addCommit(t *testing.T, dir, msg string) {
	t.Helper()
	cmds := [][]string{
		{"git", "-C", dir, "commit", "--allow-empty", "-m", msg},
	}
	for _, args := range cmds {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
}

func TestGitAdapter_DetectsNewCommit(t *testing.T) {
	dir := initRepo(t)

	a := gitadapter.New(dir)
	a.PollInterval = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	// Let one poll run (sees no new commits)
	time.Sleep(200 * time.Millisecond)
	assert.Empty(t, signals)

	// Add a commit — adapter should detect it on next poll
	addCommit(t, dir, "test: add something")
	time.Sleep(300 * time.Millisecond)

	require.Len(t, signals, 1)
	sig := <-signals
	assert.Equal(t, adapter.SignalGitCommit, sig.Type)
	assert.Equal(t, "git", sig.Source)
	assert.NotEmpty(t, sig.Payload["hash"])
	assert.Equal(t, dir, sig.Payload["repo"])
}

func TestGitAdapter_NoDuplicates(t *testing.T) {
	dir := initRepo(t)
	addCommit(t, dir, "pre-existing commit")

	a := gitadapter.New(dir)
	a.PollInterval = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 10)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	// Wait for two polls — should see no signals (commit was already there)
	time.Sleep(350 * time.Millisecond)
	assert.Empty(t, signals, "pre-existing commit should not trigger a signal")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/git/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Implement git.go**

Create `internal/adapter/git/git.go`:

```go
package git

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
)

type GitAdapter struct {
	RepoPath     string
	PollInterval time.Duration
	lastSeen     string
	stopCh       chan struct{}
}

func New(repoPath string) *GitAdapter {
	return &GitAdapter{
		RepoPath:     repoPath,
		PollInterval: 30 * time.Second,
		stopCh:       make(chan struct{}),
	}
}

func (g *GitAdapter) Name() string { return "git" }

func (g *GitAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	// Record HEAD on startup so existing commits are not reported
	if head, err := g.headHash(); err == nil {
		g.lastSeen = head
	}

	go func() {
		ticker := time.NewTicker(g.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-g.stopCh:
				return
			case <-ticker.C:
				g.poll(out)
			}
		}
	}()
	return nil
}

func (g *GitAdapter) Stop() error {
	close(g.stopCh)
	return nil
}

func (g *GitAdapter) poll(out chan<- adapter.Signal) {
	commits, err := g.newCommits()
	if err != nil || len(commits) == 0 {
		return
	}
	for _, hash := range commits {
		out <- adapter.Signal{
			Type:   adapter.SignalGitCommit,
			Source: "git",
			Payload: map[string]any{
				"hash": hash,
				"repo": g.RepoPath,
			},
		}
	}
	// Most recent commit is first in git log output
	g.lastSeen = commits[0]
}

func (g *GitAdapter) newCommits() ([]string, error) {
	var args []string
	if g.lastSeen == "" {
		args = []string{"-C", g.RepoPath, "log", "--format=%H", "-1"}
	} else {
		args = []string{"-C", g.RepoPath, "log", "--format=%H", g.lastSeen + "..HEAD"}
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

func (g *GitAdapter) headHash() (string, error) {
	out, err := exec.Command("git", "-C", g.RepoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/adapter/git/... -v
```

Expected: PASS — both tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/git/
git commit -m "feat: git adapter — polls for new commits"
```

---

### Task 7: Schedule Adapter

**Files:**
- Create: `internal/adapter/schedule/schedule.go`
- Create: `internal/adapter/schedule/schedule_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/schedule/schedule_test.go`:

```go
package schedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/adapter/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleAdapter_Fires(t *testing.T) {
	// Every second cron expression
	a, err := schedule.New("* * * * * *") // 6-field: fires every second
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	// Should fire at least once within 3 seconds
	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalScheduleTick, sig.Type)
		assert.Equal(t, "schedule", sig.Source)
	case <-ctx.Done():
		t.Fatal("no signal received within timeout")
	}
}

func TestScheduleAdapter_InvalidCron(t *testing.T) {
	_, err := schedule.New("not-a-cron")
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/adapter/schedule/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Implement schedule.go**

Create `internal/adapter/schedule/schedule.go`:

```go
package schedule

import (
	"context"
	"fmt"

	"github.com/robfig/cron/v3"
	"github.com/sausheong/sidecar/internal/adapter"
)

type ScheduleAdapter struct {
	expr string
	c    *cron.Cron
}

func New(expr string) (*ScheduleAdapter, error) {
	// Parse with seconds support for testing; standard 5-field cron for prod
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(expr); err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return &ScheduleAdapter{expr: expr}, nil
}

func (s *ScheduleAdapter) Name() string { return "schedule" }

func (s *ScheduleAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	s.c = cron.New(cron.WithSeconds())
	_, err := s.c.AddFunc(s.expr, func() {
		select {
		case <-ctx.Done():
		case out <- adapter.Signal{
			Type:    adapter.SignalScheduleTick,
			Source:  "schedule",
			Payload: map[string]any{},
		}:
		}
	})
	if err != nil {
		return fmt.Errorf("adding cron job: %w", err)
	}
	s.c.Start()
	return nil
}

func (s *ScheduleAdapter) Stop() error {
	if s.c != nil {
		s.c.Stop()
	}
	return nil
}
```

- [ ] **Step 4: Fetch cron dependency and run tests**

```bash
go get github.com/robfig/cron/v3
go mod tidy
go test ./internal/adapter/schedule/... -v
```

Expected: PASS — both tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/schedule/ go.mod go.sum
git commit -m "feat: schedule adapter — cron-driven signals"
```

---

### Task 8: Git Output

**Files:**
- Create: `internal/output/output.go`
- Create: `internal/output/output_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/output/output_test.go`:

```go
package output_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sausheong/sidecar/internal/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}

func TestCommitBranch_NoChanges(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)

	branch, err := o.CommitBranch("task-abc", "sidecar: test task")
	require.NoError(t, err)
	assert.Equal(t, output.BranchNoChanges, branch)
}

func TestCommitBranch_WithChanges(t *testing.T) {
	dir := initRepo(t)
	o := output.New(dir)

	// Write a file to simulate agent output
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("patched"), 0644))

	branch, err := o.CommitBranch("task-xyz", "sidecar: applied fix")
	require.NoError(t, err)
	assert.Equal(t, "sidecar/task-xyz", branch)

	// Verify the branch exists with the commit
	out, err := exec.Command("git", "-C", dir, "log", "--oneline", "sidecar/task-xyz").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "sidecar: applied fix")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/output/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Implement output.go**

Create `internal/output/output.go`:

```go
package output

import (
	"fmt"
	"os/exec"
	"strings"
)

const BranchNoChanges = ""

type Output struct {
	repoPath string
}

func New(repoPath string) *Output {
	return &Output{repoPath: repoPath}
}

// CommitBranch creates a sidecar/<taskID> branch, stages all changes, and
// commits them. Returns BranchNoChanges if the working tree is clean.
func (o *Output) CommitBranch(taskID, message string) (string, error) {
	if clean, err := o.isClean(); err != nil {
		return "", err
	} else if clean {
		return BranchNoChanges, nil
	}

	branch := "sidecar/" + taskID
	cmds := [][]string{
		{"git", "-C", o.repoPath, "checkout", "-b", branch},
		{"git", "-C", o.repoPath, "add", "-A"},
		{"git", "-C", o.repoPath, "commit", "-m", message},
		{"git", "-C", o.repoPath, "checkout", "-"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %s: %w\n%s", args[2], err, out)
		}
	}
	return branch, nil
}

func (o *Output) isClean() (bool, error) {
	out, err := exec.Command("git", "-C", o.repoPath, "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return strings.TrimSpace(string(out)) == "", nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/output/... -v
```

Expected: PASS — both tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/output/
git commit -m "feat: git output — commits changes to sidecar/<task-id> branch"
```

---

### Task 9: Improvement Loop

**Files:**
- Create: `internal/loop/loop.go`
- Create: `internal/loop/loop_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/loop/loop_test.go`:

```go
package loop_test

import (
	"context"
	"testing"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/loop"
	"github.com/stretchr/testify/assert"
)

func TestBuildSystemPrompt_GitCommit(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalGitCommit,
		Source:  "git",
		Payload: map[string]any{"hash": "abc123", "repo": "/tmp/myrepo"},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "abc123")
	assert.Contains(t, prompt, "engineering agent")
}

func TestBuildSystemPrompt_ScheduleTick(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalScheduleTick,
		Source:  "schedule",
		Payload: map[string]any{},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "proactive")
	assert.Contains(t, prompt, "engineering agent")
}

func TestBuildSystemPrompt_OnDemand(t *testing.T) {
	sig := adapter.Signal{
		Type:    adapter.SignalOnDemand,
		Source:  "cli",
		Payload: map[string]any{"description": "fix the flaky test in auth_test.go"},
	}
	prompt := loop.BuildSystemPrompt(sig)
	assert.Contains(t, prompt, "fix the flaky test")
}

func TestDefaultModels(t *testing.T) {
	cfg := &config.Config{}
	m := loop.ResolveModels(cfg)
	assert.NotEmpty(t, m.Coding)
	assert.NotEmpty(t, m.Triage)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/loop/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Implement loop.go**

Create `internal/loop/loop.go`:

```go
package loop

import (
	"context"
	"fmt"
	"os"

	"github.com/sausheong/harness/providers/anthropic"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tools/bash"
	"github.com/sausheong/harness/tools/file"
	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/output"
	"github.com/sausheong/sidecar/internal/store"
)

type Models struct {
	Coding string
	Triage string
}

func ResolveModels(cfg *config.Config) Models {
	m := Models{
		Coding: "anthropic/claude-sonnet-4-6",
		Triage: "anthropic/claude-haiku-4-5-20251001",
	}
	if cfg.Models.Coding != "" {
		m.Coding = cfg.Models.Coding
	}
	if cfg.Models.Triage != "" {
		m.Triage = cfg.Models.Triage
	}
	return m
}

type Loop struct {
	db        *store.DB
	workspace *store.Workspace
	cfg       *config.Config
	repoPath  string
}

func New(db *store.DB, workspace *store.Workspace, cfg *config.Config, repoPath string) *Loop {
	return &Loop{db: db, workspace: workspace, cfg: cfg, repoPath: repoPath}
}

func (l *Loop) Run(ctx context.Context, sig adapter.Signal) error {
	task := &store.Task{
		WorkspaceID: l.workspace.ID,
		SignalType:  string(sig.Type),
		Summary:     summarize(sig),
	}
	if err := l.db.CreateTask(ctx, task); err != nil {
		return fmt.Errorf("creating task: %w", err)
	}

	if err := l.db.UpdateTaskStatus(ctx, task.ID, "running"); err != nil {
		return err
	}

	models := ResolveModels(l.cfg)
	systemPrompt := BuildSystemPrompt(sig)

	reg := tool.NewRegistry()
	reg.Register(&file.ReadFileTool{WorkDir: l.repoPath})
	reg.Register(&file.WriteFileTool{WorkDir: l.repoPath})
	reg.Register(&file.EditFileTool{WorkDir: l.repoPath})
	reg.Register(&bash.BashTool{WorkDir: l.repoPath})

	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: anthropic.NewAnthropicProvider(os.Getenv("ANTHROPIC_API_KEY"), ""),
			Tools:    reg,
			Session:  session.NewSession(task.ID.String(), "main"),
		},
		runtime.AgentSpec{
			ID:           task.ID.String(),
			Name:         "Sidecar",
			Model:        models.Coding,
			Workspace:    l.repoPath,
			SystemPrompt: systemPrompt,
			MaxTurns:     20,
		},
	)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, "failed")
		return fmt.Errorf("building runtime: %w", err)
	}
	defer rt.Close()

	events, err := rt.Run(ctx, userMessage(sig), nil)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, "failed")
		return err
	}
	for range events {
		// drain — results are in the filesystem
	}

	out := output.New(l.repoPath)
	branch, err := out.CommitBranch(task.ID.String(), "sidecar: "+task.Summary)
	if err != nil {
		_ = l.db.UpdateTaskStatus(ctx, task.ID, "failed")
		return err
	}

	status := "completed"
	if branch != "" {
		status = "committed:" + branch
	}
	return l.db.UpdateTaskStatus(ctx, task.ID, status)
}

func BuildSystemPrompt(sig adapter.Signal) string {
	base := `You are an autonomous engineering agent (Sidecar) attached to a software project.
Your job is to improve, fix, and maintain the codebase. You have access to the filesystem and bash.
Make targeted, minimal changes. Run tests after any code change to verify correctness.
Only modify files relevant to the current task.`

	switch sig.Type {
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		return fmt.Sprintf(`%s

A new commit (%s) was just pushed. Review it and fix any immediate issues:
broken tests, compilation errors, obvious bugs introduced by the change.
If everything looks good, do nothing.`, base, hash)

	case adapter.SignalScheduleTick:
		return fmt.Sprintf(`%s

This is a proactive maintenance sweep. Look for improvement opportunities:
- Stale or vulnerable dependencies
- Missing test coverage for existing code paths
- Outdated documentation
- Dead code or unused imports
Pick one meaningful improvement and apply it.`, base)

	default:
		desc, _ := sig.Payload["description"].(string)
		return fmt.Sprintf(`%s

On-demand task: %s

Complete this task. Run tests to verify your changes.`, base, desc)
	}
}

func userMessage(sig adapter.Signal) string {
	switch sig.Type {
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		return fmt.Sprintf("New commit detected: %s. Review and fix any issues.", hash)
	case adapter.SignalScheduleTick:
		return "Proactive sweep: identify and apply one meaningful improvement."
	default:
		desc, _ := sig.Payload["description"].(string)
		return desc
	}
}

func summarize(sig adapter.Signal) string {
	switch sig.Type {
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		if len(hash) > 8 {
			hash = hash[:8]
		}
		return "review commit " + hash
	case adapter.SignalScheduleTick:
		return "proactive sweep"
	default:
		desc, _ := sig.Payload["description"].(string)
		if len(desc) > 60 {
			return desc[:60] + "..."
		}
		return desc
	}
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/loop/... -v
```

Expected: PASS — all four tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/loop/
git commit -m "feat: improvement loop wrapping Harness runtime"
```

---

### Task 10: Daemon

**Files:**
- Create: `internal/daemon/daemon.go`

- [ ] **Step 1: Write the test**

Create `internal/daemon/daemon_test.go`:

```go
package daemon_test

import (
	"context"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAdapter struct {
	name    string
	signals []adapter.Signal
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) Stop() error  { return nil }
func (f *fakeAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	go func() {
		for _, sig := range f.signals {
			select {
			case <-ctx.Done():
				return
			case out <- sig:
			}
		}
	}()
	return nil
}

func TestDaemon_RoutesSignals(t *testing.T) {
	received := make([]adapter.Signal, 0)
	handler := func(ctx context.Context, sig adapter.Signal) error {
		received = append(received, sig)
		return nil
	}

	adapters := []adapter.Adapter{
		&fakeAdapter{name: "fake", signals: []adapter.Signal{
			{Type: adapter.SignalGitCommit, Source: "fake"},
			{Type: adapter.SignalScheduleTick, Source: "fake"},
		}},
	}

	d := daemon.New(adapters, handler)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, d.Start(ctx))
	time.Sleep(200 * time.Millisecond)
	d.Stop()

	assert.Len(t, received, 2)
	assert.Equal(t, adapter.SignalGitCommit, received[0].Type)
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/daemon/... -v
```

Expected: FAIL — package not found.

- [ ] **Step 3: Implement daemon.go**

Create `internal/daemon/daemon.go`:

```go
package daemon

import (
	"context"
	"log"

	"github.com/sausheong/sidecar/internal/adapter"
)

type Handler func(ctx context.Context, sig adapter.Signal) error

type Daemon struct {
	adapters []adapter.Adapter
	handler  Handler
	signals  chan adapter.Signal
	done     chan struct{}
}

func New(adapters []adapter.Adapter, handler Handler) *Daemon {
	return &Daemon{
		adapters: adapters,
		handler:  handler,
		signals:  make(chan adapter.Signal, 64),
		done:     make(chan struct{}),
	}
}

func (d *Daemon) Start(ctx context.Context) error {
	for _, a := range d.adapters {
		if err := a.Start(ctx, d.signals); err != nil {
			return err
		}
	}
	go d.run(ctx)
	return nil
}

func (d *Daemon) Stop() {
	for _, a := range d.adapters {
		if err := a.Stop(); err != nil {
			log.Printf("stopping adapter %s: %v", a.Name(), err)
		}
	}
	close(d.done)
}

func (d *Daemon) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.done:
			return
		case sig := <-d.signals:
			if err := d.handler(ctx, sig); err != nil {
				log.Printf("handler error for signal %s: %v", sig.Type, err)
			}
		}
	}
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/daemon/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/
git commit -m "feat: daemon routes adapter signals to improvement loop"
```

---

### Task 11: `sidecar attach` Command

**Files:**
- Modify: `internal/cli/attach.go`

- [ ] **Step 1: Write the test**

Create `internal/cli/attach_test.go`:

```go
package cli_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestAttachCmd_RequiresYAML(t *testing.T) {
	root := cli.RootCmd()
	root.SetArgs([]string{"attach", "/nonexistent/path"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sidecar.yaml")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/cli/... -v -run TestAttachCmd
```

Expected: FAIL — attach command returns nil, not an error.

- [ ] **Step 3: Implement attach.go**

Replace `internal/cli/attach.go`:

```go
package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/sausheong/sidecar/internal/adapter"
	gitadapter "github.com/sausheong/sidecar/internal/adapter/git"
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

			ws := &store.Workspace{
				Name:       cfg.Workspace.Name,
				Path:       abs,
				ConfigHash: cfgPath,
			}
			if err := db.UpsertWorkspace(ctx, ws); err != nil {
				return fmt.Errorf("upserting workspace: %w", err)
			}

			adapters := buildAdapters(abs, cfg)
			l := loop.New(db, ws, cfg, abs)
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
		}
	}
	return adapters
}
```

- [ ] **Step 4: Run the test**

```bash
go test ./internal/cli/... -v -run TestAttachCmd
```

Expected: PASS.

- [ ] **Step 5: Build and verify the binary works**

```bash
go build -o /tmp/sidecar ./cmd/sidecar
/tmp/sidecar attach --help
```

Expected: help text printed, no crash.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/attach.go internal/cli/attach_test.go
git commit -m "feat: sidecar attach command starts daemon"
```

---

### Task 12: `sidecar task` Command

**Files:**
- Modify: `internal/cli/task.go`

- [ ] **Step 1: Write the test**

Create `internal/cli/task_test.go`:

```go
package cli_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestTaskCmd_RequiresDescription(t *testing.T) {
	root := cli.RootCmd()
	root.SetArgs([]string{"task"})
	err := root.Execute()
	assert.Error(t, err)
}

func TestTaskCmd_RequiresDBURL(t *testing.T) {
	root := cli.RootCmd()
	root.SetArgs([]string{"task", "fix the tests", "--repo", "/nonexistent"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/cli/... -v -run TestTaskCmd
```

Expected: FAIL — task command does not error on missing DB URL.

- [ ] **Step 3: Implement task.go**

Replace `internal/cli/task.go`:

```go
package cli

import (
	"context"
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
				cfg = &config.Config{} // use defaults if no sidecar.yaml
			}

			ws := &store.Workspace{
				Name:       filepath.Base(abs),
				Path:       abs,
				ConfigHash: "none",
			}
			if err := db.UpsertWorkspace(ctx, ws); err != nil {
				return err
			}

			sig := adapter.Signal{
				Type:   adapter.SignalOnDemand,
				Source: "cli",
				Payload: map[string]any{
					"description": description,
				},
			}

			l := loop.New(db, ws, cfg, abs)
			log.Printf("Running task: %s", description)
			return l.Run(ctx, sig)
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "path to the target repository (default: .)")
	return cmd
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/cli/... -v -run TestTaskCmd
```

Expected: PASS — both tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/task.go internal/cli/task_test.go
git commit -m "feat: sidecar task command for on-demand tasks"
```

---

### Task 13: `sidecar status` Command

**Files:**
- Modify: `internal/cli/status.go`

- [ ] **Step 1: Write the test**

Create `internal/cli/status_test.go`:

```go
package cli_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestStatusCmd_RequiresDBURL(t *testing.T) {
	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"status"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/cli/... -v -run TestStatusCmd
```

Expected: FAIL — status returns nil.

- [ ] **Step 3: Implement status.go**

Replace `internal/cli/status.go`:

```go
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
		Short: "Show recent sidecar tasks",
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
				return err
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
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/cli/... -v -run TestStatusCmd
```

Expected: PASS.

- [ ] **Step 5: Full build and test run**

```bash
go build ./...
go test ./... -v
```

Expected: all unit tests pass; integration tests skip unless `SIDECAR_TEST_DB_URL` is set.

- [ ] **Step 6: Final commit**

```bash
git add internal/cli/status.go internal/cli/status_test.go
git commit -m "feat: sidecar status command shows recent tasks"
```

---

## Verification

After all tasks are complete, verify the full flow:

```bash
# 1. Ensure PostgreSQL is running with a sidecar database
createdb sidecar 2>/dev/null || true
export SIDECAR_DB_URL="postgres://localhost/sidecar"
export ANTHROPIC_API_KEY="..."

# 2. Build
go build -o /tmp/sidecar ./cmd/sidecar

# 3. Attach to a test repo and watch it pick up a commit
cd /tmp && git init test-repo && cd test-repo
cat > sidecar.yaml << 'EOF'
workspace:
  name: test-repo
signals:
  - adapter: git
    watch: [push]
  - adapter: schedule
    cron: "0 * * * *"
autonomy:
  test_fixes: auto-commit
  bug_fixes: pull-request
EOF
git add sidecar.yaml && git commit -m "initial"

# 4. Start sidecar in one terminal
/tmp/sidecar attach .

# 5. In another terminal, submit an on-demand task
/tmp/sidecar task "add a hello.txt file with the content 'hello world'" --repo /tmp/test-repo

# 6. Check status
/tmp/sidecar status --repo /tmp/test-repo
```

Expected: status shows a completed task; `git branch -a` shows a `sidecar/<uuid>` branch with the change.
