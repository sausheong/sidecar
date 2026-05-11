# Sidecar

An autonomous engineering agent that attaches to any software project as a persistent sidecar process and continuously maintains it. Sidecar watches for signals (git commits, CI failures, scheduled sweeps, log anomalies, metric alerts), triages each one, and applies the appropriate fix — committing directly, opening a PR, or recording a suggestion — based on your configured autonomy level.

## How It Works

```
Signal source (git / CI / schedule / logs / metrics)
        ↓
    Triage (Haiku — should we act? what kind of change?)
        ↓
  Memory retrieval (pgvector — what do we know about this codebase?)
        ↓
   Coding agent (Claude — read, edit, bash, run tests)
        ↓
  Output routing (auto-commit / pull-request / suggest-only)
        ↓
   Reflect (Haiku — extract learnings, update memory)
```

Sidecar gets smarter over time: after every task it extracts architectural facts, workflow patterns, and engineering history from the work it did, storing them as vector-indexed memory. The next task starts with that context injected into the agent's prompt.

## Prerequisites

- Go 1.25+
- PostgreSQL with the [pgvector](https://github.com/pgvector/pgvector) extension
- An Anthropic API key (Claude models for coding; Haiku for triage and reflect)
- For memory: an OpenAI or Voyage AI API key (embeddings)

## Build

```bash
git clone https://github.com/sausheong/sidecar
cd sidecar
go build -o sidecar ./cmd/sidecar
```

## Database Setup

```sql
-- In your PostgreSQL instance:
CREATE DATABASE sidecar;
\c sidecar
CREATE EXTENSION IF NOT EXISTS vector;
```

Sidecar runs migrations automatically on `attach`. Point it at your database:

```bash
export SIDECAR_DB_URL="postgres://user:password@localhost/sidecar"
```

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `SIDECAR_DB_URL` | Always | PostgreSQL connection string |
| `ANTHROPIC_API_KEY` | Always | Claude API key |
| `OPENAI_API_KEY` | When `embedding.provider: openai` | OpenAI embeddings |
| `VOYAGE_API_KEY` | When `embedding.provider: voyage` | Voyage AI embeddings |
| `GITHUB_TOKEN` | For GitHub CI adapter / PR creation | GitHub personal access token |
| `GITLAB_TOKEN` | For GitLab CI adapter | GitLab personal access token |
| `CIRCLECI_TOKEN` | For CircleCI adapter | CircleCI API token |
| `DATADOG_API_KEY` | For Datadog metrics adapter | Datadog API key |
| `DATADOG_APP_KEY` | For Datadog metrics adapter | Datadog application key |

## sidecar.yaml

Place a `sidecar.yaml` in the root of the project you want Sidecar to manage.

```yaml
workspace:
  name: "my-project"
  language: "go"

# Signal sources — Sidecar watches all of them concurrently
signals:
  # Watch for new git commits
  - adapter: git

  # Proactive sweep on a schedule (6-field cron: sec min hour dom mon dow)
  - adapter: schedule
    cron: "0 0 2 * * *"   # 2am daily

  # React to GitHub Actions CI failures
  - adapter: github-ci
    repo: "owner/repo"
    token: $GITHUB_TOKEN
    poll_interval: "60s"
    watch:
      - "failure"
      - "timed_out"

  # React to GitLab CI failures
  - adapter: gitlab-ci
    repo: "group/project"
    token: $GITLAB_TOKEN
    poll_interval: "60s"
    watch:
      - "failed"
      - "canceled"

  # React to CircleCI failures
  - adapter: circleci
    repo: "gh/org/repo"
    token: $CIRCLECI_TOKEN
    poll_interval: "60s"
    watch:
      - "failed"
      - "error"

  # Watch log files and processes for anomalies
  - adapter: logs
    poll_interval: "2s"
    logs:
      files:
        - path: "logs/app.log"
      processes:
        - command: "make serve"
      patterns:
        - match: "ERROR"
          quiet_period: "5m"
        - match: "PANIC"
          quiet_period: "10m"
      rate:
        window: "30s"
        threshold: 10
        quiet_period: "2m"

  # Watch Datadog monitors
  - adapter: metrics
    token: $DATADOG_API_KEY
    poll_interval: "60s"
    metrics:
      provider: datadog
      tags:
        - "env:production"

  # Watch Prometheus alerting rules
  - adapter: metrics
    poll_interval: "30s"
    metrics:
      provider: prometheus
      endpoint: "http://localhost:9090"

# How much autonomy Sidecar has per change type
autonomy:
  dependency_updates: auto-commit
  test_fixes: auto-commit
  bug_fixes: pull-request
  refactoring: suggest-only
  schema_changes: suggest-only
  log_fixes: suggest-only
  metric_fixes: suggest-only

# Model overrides (defaults shown)
models:
  coding: "anthropic/claude-sonnet-4-6"
  triage: "anthropic/claude-haiku-4-5-20251001"

# Scope (optional) — restrict which files the agent can touch
scope:
  include:
    - "internal/**"
    - "cmd/**"
  exclude:
    - "vendor/**"

# Embedding for persistent memory (optional but recommended)
embedding:
  provider: openai        # "openai" | "voyage"
  model: text-embedding-3-small
```

### Autonomy Levels

| Level | Behaviour |
|-------|-----------|
| `auto-commit` | Agent commits changes directly to the current branch |
| `pull-request` | Agent commits to a new branch and opens a PR |
| `suggest-only` | Agent writes up what it would do; no code is changed |

## Commands

### `sidecar attach [path]`

Start the sidecar daemon for a project. Reads `sidecar.yaml` from `path` (default: current directory), runs database migrations, registers the workspace, and starts all configured adapters.

```bash
export SIDECAR_DB_URL="postgres://localhost/sidecar"
export ANTHROPIC_API_KEY="sk-ant-..."

sidecar attach .
sidecar attach /path/to/project
```

Stop with `Ctrl+C` or `SIGTERM`.

### `sidecar task <description>`

Submit a one-off on-demand task without starting the full daemon. The agent runs once and exits.

```bash
sidecar task "add pagination to the /users endpoint"
sidecar task "fix the flaky test in auth_test.go" --repo /path/to/project
```

### `sidecar status`

Show the 20 most recent tasks for the workspace.

```bash
sidecar status
sidecar status --repo /path/to/project
```

```
STATUS     SIGNAL           SUMMARY                          CREATED
completed  git.commit       review commit abc12345           2026-05-09 14:32:01
suggested  ci.failure       fix CI failure in CI @ def456    2026-05-09 13:15:44
skipped    schedule.tick    proactive sweep                  2026-05-09 02:00:01
```

### `sidecar ask <question>`

Query the workspace's accumulated memory using natural language. Requires embedding to be configured.

```bash
sidecar ask "how does authentication work?"
sidecar ask "which areas are most fragile?" --repo /path/to/project
```

## Memory System

When `embedding` is configured in `sidecar.yaml`, Sidecar maintains a persistent vector memory for each workspace:

- **After every task:** a Haiku reflect step extracts architectural facts (semantic), workflow patterns (procedural), and a task summary (episodic) from what the agent did, storing them as 1024-dimensional embeddings in PostgreSQL via pgvector.
- **Before every task:** the most relevant memory entries are retrieved by semantic similarity and injected into the agent's system prompt.
- **Scheduled sweeps:** the idle sweep prioritises fragile areas and under-tested code paths from memory before falling back to generic checks.
- **`sidecar ask`:** retrieves all three memory categories and synthesises a natural-language answer with a single Haiku call.

### Supported Embedding Providers

| Provider | Key | Model |
|----------|-----|-------|
| OpenAI (default) | `OPENAI_API_KEY` | `text-embedding-3-small` (1024 dims) |
| Voyage AI | `VOYAGE_API_KEY` | `voyage-4` (1024 dims native) |

## Signal Adapters

| Adapter | `adapter:` value | What it watches |
|---------|-----------------|-----------------|
| Git | `git` | New commits pushed to the repo |
| Schedule | `schedule` | Cron-triggered maintenance sweeps |
| GitHub CI | `github-ci` | Failed GitHub Actions workflow runs |
| GitLab CI | `gitlab-ci` | Failed GitLab CI pipelines |
| CircleCI | `circleci` | Failed CircleCI workflow runs |
| Logs | `logs` | Keyword matches and rate spikes in log files or process output |
| Datadog | `metrics` + `provider: datadog` | Triggered Datadog monitors |
| Prometheus | `metrics` + `provider: prometheus` | Firing Prometheus alerting rules |

## Example Workflow

```bash
# 1. Set up environment
export SIDECAR_DB_URL="postgres://localhost/sidecar"
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."        # for memory

# 2. Add sidecar.yaml to your project
cat > myproject/sidecar.yaml << 'EOF'
workspace:
  name: myproject
signals:
  - adapter: git
  - adapter: schedule
    cron: "0 0 2 * * *"
autonomy:
  test_fixes: auto-commit
  bug_fixes: pull-request
embedding:
  provider: openai
EOF

# 3. Start the daemon
cd myproject
sidecar attach .

# 4. Submit a one-off task
sidecar task "add rate limiting to the API"

# 5. Check status
sidecar status

# 6. Ask about the codebase (after some tasks have run)
sidecar ask "what are the most fragile parts of this codebase?"
```

## Demo Project

`examples/webapp/` is a minimal Go task-tracker API purpose-built to exercise every Sidecar signal adapter. It ships with seeded bugs, intentionally failing tests, and an error-generating endpoint so you can see Sidecar react and fix things out of the box.

| Seeded issue | Sidecar adapter that catches it |
|---|---|
| `DELETE /tasks/{id}` returns 200 instead of 204 | CI adapter (failing test) |
| `POST /tasks` accepts empty title | CI adapter (failing test) |
| Missing test for `PUT /tasks/{id}` | Schedule adapter (nightly sweep) |
| `POST /demo/stress` generates ERROR log bursts | Logs adapter |
| `HighErrorRate` Prometheus alerting rule | Metrics adapter |

**Quick start:**

```bash
# 1. Start the webapp
cd examples/webapp
go run ./cmd/webapp          # listens on :8080

# 2. Attach Sidecar (in another terminal, from examples/webapp/)
export SIDECAR_DB_URL="postgres://sidecar:sidecar@localhost:5433/sidecar"
export ANTHROPIC_API_KEY="sk-ant-..."
sidecar attach .

# 3. Trigger the logs adapter — 6 hits in 30 seconds
for i in $(seq 1 6); do curl -s -X POST http://localhost:8080/demo/stress; done

# 4. Run an on-demand task
sidecar task "review test coverage and add missing tests" --repo .
```

See `examples/webapp/README.md` for the full guide including Prometheus setup and how to wire the GitHub CI adapter.

## Architecture Notes

- Built on [harness](https://github.com/sausheong/harness) — a Go library for building LLM agents
- All state is in PostgreSQL; the daemon is stateless and restartable
- The `sidecar.yaml` in the target repo is the only coupling between the software and its agent — no code changes required in the managed project
- Each signal triggers an independent task; tasks do not interfere with each other
- The coding agent has access to `read_file`, `write_file`, `edit_file`, and `bash` tools; write tools are gated by autonomy level
# test
