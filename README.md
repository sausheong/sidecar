# Sidecar

An autonomous engineering agent that attaches to any software project as a persistent sidecar process and continuously maintains it. Sidecar watches for signals (git commits, CI failures, scheduled sweeps, log anomalies, metric alerts, uptime checks), triages each one, and applies the appropriate fix — committing directly, opening a PR, recording a suggestion, or notifying a human — based on your configured autonomy level. Before any change ships, an independent adversarial evaluator runs the tests over the diff and can veto it.

## How It Works

```
Signal source (git / CI / schedule / logs / metrics / uptime)
        ↓
    Triage (Haiku — should we act? what kind of change?)
        ↓
  Budget gate (skip if the daily token cap is already spent)
        ↓
  Memory retrieval (pgvector — what do we know about this codebase?)
        ↓
   Worktree (isolated git worktree for this task)
        ↓
   Coding agent (Claude — read, edit, bash, run tests; skills loaded)
        ↓
  Evaluator (fresh skeptic — runs tests on the diff, PASS or REJECT)
        ↓
  Output routing (auto-commit / pull-request / suggest-only / notify)
        ↓
   Reflect (Haiku — extract learnings, update memory)
```

For infrastructure-class failures (e.g. an uptime check where DNS or TCP is down) the loop short-circuits at triage to a notification — no coding agent runs. The `notify` autonomy level fires Slack / webhook / email instead of touching code.

### 1. Signal adapters

Adapters run concurrently inside the daemon and fan signals into a central channel. Each adapter polls or watches its source and emits a typed `Signal` when something noteworthy happens.

- **Git** — polls `git log` every 30 seconds. Records the HEAD commit at startup so only _new_ commits trigger the loop; pre-existing history is never reprocessed.
- **GitHub / GitLab / CircleCI CI** — polls the provider API for failed or timed-out workflow runs on a configurable interval.
- **Schedule** — fires a maintenance sweep on a cron expression (e.g. `0 0 2 * * *` for 2am daily). Uses [robfig/cron](https://github.com/robfig/cron) with second-precision support.
- **Logs** — tails one or more log files and optionally spawns a subprocess, scanning each new line against keyword patterns (armed/disarmed with a quiet period) and a sliding-window rate threshold.
- **Metrics** — polls Prometheus or Datadog for firing alerting rules. For Prometheus it queries `/api/v1/rules`; for Datadog it calls the monitors API filtered by configured tags.
- **Uptime** — polls a list of HTTP endpoints on a configurable interval, emitting a signal when any endpoint is unreachable, returns an unexpected status code, or exceeds a latency threshold (`expect_max_ms`).

On-demand tasks (`sidecar task "…"`) bypass the adapter layer entirely and inject a signal directly into the loop.

### 2. Triage

Every signal passes through a single-turn Haiku agent before any code is touched. The triage agent receives a summary of the signal (commit stat, log line, CI URL, alert name) and returns a structured JSON decision:

```json
{"should_act": true, "change_type": "bug_fix", "reason": "one sentence"}
```

Valid `change_type` values: `test_fix`, `bug_fix`, `dependency_update`, `refactor`, `log_fix`, `metric_fix`, `uptime_fix`, `unknown`.

If `should_act` is false the task is recorded as `skipped` and no agent is run. If the triage call fails for any reason (network error, malformed JSON, empty response) Sidecar falls back to a conservative default: act, but with `suggest-only` autonomy. On-demand tasks always bypass triage.

### 3. Memory retrieval

If an embedding provider is configured, Sidecar queries pgvector for the top-5 most relevant memory entries before building the agent's system prompt. Relevance is measured by cosine similarity against the task description.

Three memory categories are stored and retrieved independently:
- **Semantic** — architectural facts about the codebase ("the auth layer is in `internal/auth`")
- **Procedural** — workflow patterns ("always run `make lint` before committing")
- **Episodic** — what happened in past tasks ("fixed a nil-pointer in handler.go in task abc123")

Retrieved entries are prepended to the system prompt so the coding agent already knows the codebase's shape before it reads a single file.

### 4. Coding agent

The coding agent is a multi-turn Claude Sonnet session with access to four tools, gated by autonomy level:

| Tool | Always available | `suggest-only` | `pull-request` / `auto-commit` |
|------|-----------------|----------------|-------------------------------|
| `read_file` | ✓ | ✓ | ✓ |
| `bash` | ✓ | ✓ | ✓ |
| `write_file` | — | — | ✓ |
| `edit_file` | — | — | ✓ |

The system prompt is signal-specific (different instructions for a git commit vs a log anomaly vs a scheduled sweep) and includes:
- The workspace root path so the agent uses absolute file paths from the first turn
- Retrieved memory context
- An instruction to start exploration with `find <workspace> -name "*.go" | head -40` (or equivalent) rather than guessing filenames

The agent runs up to 20 turns inside an **isolated git worktree** dedicated to this task, so concurrent signals never share a working tree. If a `.sidecar/skills/` directory exists in the target repo, its `SKILL.md` files are loaded as **skills** — persistent project knowledge the agent can pull in on demand instead of re-deriving it every run. Typical task flow: discover the repo layout → read relevant files → run the test suite → identify the failure → apply a targeted fix → run tests again to confirm.

### 5. Evaluator gate

Before any code ships (`auto-commit` or `pull-request`), an **adversarial evaluator** reviews the diff. It is a fresh agent session — different context from the coding agent, instructed to assume the change is broken until proven otherwise — with read and bash tools only. It runs the build and tests against the diff (anchored to the worktree's base ref, so it sees the change even if the coding agent committed it itself) and returns a verdict:

```json
{"pass": false, "reasons": "tests still fail: TestCreate expects 201, got 200"}
```

On **REJECT**, the change does not ship — it is recorded as a suggestion with the evaluator's reasons. The gate **fails closed**: if the evaluator errors or returns unparseable output, the change is treated as rejected, never silently committed. The evaluator is enabled by default (`verification.enabled: true`) and applies to both `auto-commit` and `pull-request`. `suggest-only` and `notify` skip it.

### 6. Output routing

After the evaluator passes, the output is routed based on the autonomy level resolved for that change type:

| Autonomy level | What happens |
|---------------|--------------|
| `suggest-only` | Agent output is stored as a suggestion in the database. No files are written. No evaluator runs. |
| `pull-request` | Evaluator-approved changes are committed to a new branch (`sidecar/<task-id>`), a PR is opened against the default branch, and the PR URL is recorded. |
| `auto-commit` | Evaluator-approved changes are committed on a `sidecar/<task-id>` branch and the branch name is recorded. |
| `notify` | No coding agent runs; a Slack / webhook / email notification fires. Use this for infrastructure-class signals where no code change will help. |

In all cases the task, its events (triage, evaluation verdict, token usage, output), and the final status are written to PostgreSQL so `sidecar status` always shows a full audit trail.

### 7. Reflect

When memory is enabled and the agent completes successfully, a short Haiku reviewer session runs asynchronously. It reads the task events and the agent's output and calls the `memory` tool to persist up to three entries — one per memory category — as vector embeddings. This step is fire-and-forget; it does not block the main loop.

Sidecar gets smarter over time: each task enriches the memory store, and future tasks for the same workspace start with progressively more context about what the codebase looks like and how it behaves.

## How to Use

### Step 1 — Install Sidecar

```bash
git clone https://github.com/sausheong/sidecar
cd sidecar
go build -o /usr/local/bin/sidecar ./cmd/sidecar
```

### Step 2 — Start a database

Sidecar needs PostgreSQL with the pgvector extension for task storage and memory.

```bash
docker run -d --name sidecar-db \
  -e POSTGRES_USER=sidecar \
  -e POSTGRES_PASSWORD=sidecar \
  -e POSTGRES_DB=sidecar \
  -p 5432:5432 \
  pgvector/pgvector:pg17

export SIDECAR_DB_URL="postgres://sidecar:sidecar@localhost:5432/sidecar?sslmode=disable"
```

Migrations run automatically on first `attach`. Neon free tier also works as a managed alternative.

### Step 3 — Set your API keys

```bash
export ANTHROPIC_API_KEY="sk-ant-..."     # required — used for triage (Haiku) and coding (Sonnet)
export OPENAI_API_KEY="sk-..."            # optional — used for memory embeddings
```

### Step 4 — Add `sidecar.yaml` to your project

Place the file in the root of the repo you want Sidecar to manage. A minimal config:

```yaml
workspace:
  name: "my-api"
  language: "go"

signals:
  - adapter: git          # react to every new commit

autonomy:
  bug_fixes: pull-request
  test_fixes: auto-commit
  refactoring: suggest-only
```

Start simple — add more adapters and tighten autonomy once you trust the output.

### Step 5 — Attach

```bash
cd /path/to/your/project
sidecar attach .
```

Sidecar prints a line for each adapter it starts, then runs silently until a signal fires. Leave it running in the background (`nohup`, `systemd`, `tmux`, etc.).

```
Sidecar attached to /path/to/your/project — watching 2 adapter(s)
```

### Step 6 — Trigger your first task

Make a commit and watch sidecar react:

```bash
# Any commit triggers the git adapter
git commit -m "add user registration endpoint"

# Check what sidecar decided
sidecar status
```

Or run a task on demand without waiting for a signal:

```bash
sidecar task "add input validation to the registration handler"
sidecar task "write tests for the auth middleware"
sidecar task "find and fix any TODO comments in the codebase"
```

### Step 7 — Review the output

```bash
sidecar status
```

```
STATUS     SIGNAL           SUMMARY                                      CREATED
suggested  git.commit       review commit 3fa12c8a                       2026-05-12 08:01:15
completed  ondemand.task    add input validation to handler               2026-05-12 07:55:02
suggested  uptime.failure   fix uptime failure: /health (wrong_status)   2026-05-12 07:30:44
skipped    schedule.tick    proactive sweep                               2026-05-12 02:00:01
```

- **`completed`** — changes were committed or a PR was opened
- **`suggested`** — Sidecar wrote up what it would do; no files were changed (suggest-only autonomy)
- **`skipped`** — triage decided the signal didn't warrant action (e.g. docs-only commit)
- **`failed`** — the agent hit an error; check the logs

If autonomy is `pull-request`, review and merge the branch Sidecar opened. If it's `auto-commit`, pull the changes. If it's `suggest-only`, read the suggestion and decide whether to act on it.

### Tuning autonomy

Start with everything on `suggest-only`, observe a few tasks, then promote to `pull-request` or `auto-commit` for change types you trust:

```yaml
autonomy:
  test_fixes: auto-commit      # low risk — tests are self-verifying
  dependency_updates: auto-commit
  bug_fixes: pull-request      # review before merging
  refactoring: suggest-only    # always want a human eye on this
  schema_changes: suggest-only
```

### Adding CI integration

Once you trust the git adapter, wire in CI so Sidecar also reacts to broken builds:

```yaml
signals:
  - adapter: git
  - adapter: github-ci
    repo: "owner/repo"
    token: $GITHUB_TOKEN
    poll_interval: "60s"
    watch:
      - "failure"
      - "timed_out"
```

Sidecar will read the failing workflow run, check out the commit, run tests locally, identify the root cause, and open a fix PR — all without human intervention.

### Adding log and metric monitoring

```yaml
signals:
  - adapter: logs
    logs:
      files:
        - path: "logs/app.log"
      patterns:
        - match: "PANIC"
          quiet_period: "10m"
      rate:
        window: "30s"
        threshold: 10    # more than 10 ERROR lines in 30s → trigger
        quiet_period: "2m"

  - adapter: metrics
    metrics:
      provider: prometheus
      endpoint: "http://localhost:9090"
```

When a log pattern fires or a Prometheus alert becomes active, Sidecar investigates the relevant code path and records a suggestion (or opens a PR, depending on your autonomy config for `log_fixes` / `metric_fixes`).

### Adding uptime and performance monitoring

```yaml
signals:
  - adapter: uptime
    poll_interval: "30s"
    uptime:
      endpoints:
        - url: "https://api.example.com/health"
          timeout: "5s"
          expect_status: 200          # fire if status != 200
        - url: "https://api.example.com/search"
          timeout: "3s"
          expect_status: 200
          expect_max_ms: 500          # fire if response time > 500ms

autonomy:
  uptime_fixes: suggest-only         # or pull-request once you trust the agent
```

Three failure modes each produce a distinct signal:

| Failure | `failure_type` | What the agent receives |
|---------|---------------|------------------------|
| Can't connect | `unreachable` | URL + error message + diagnostics |
| Wrong HTTP status | `wrong_status` | URL + got/expected status codes + diagnostics |
| Too slow | `slow_response` | URL + actual ms + threshold ms + diagnostics |

#### Diagnostics

On every failure, Sidecar automatically runs a set of diagnostic checks and includes the results in the signal before triage runs. This lets the triage model decide whether the failure is an infrastructure problem (no agent needed — just notify) or a code problem (agent should investigate).

**Default checks** (run automatically when `diagnostics:` is omitted):

| Check | `check:` value | What it does |
|-------|---------------|-------------|
| DNS | `dns` | Resolves the hostname — failure indicates DNS or load-balancer problem |
| TCP | `tcp` | Opens a TCP connection to host:port — failure indicates server process down or firewall |
| TLS | `tls` | Validates the certificate and checks expiry (auto-run for `https://` only) |
| Cross-endpoint | `cross` | Probes all other configured endpoints — all down indicates infrastructure outage |

**Optional built-in checks:**

| Check | `check:` value | What it does |
|-------|---------------|-------------|
| Ping | `ping` | ICMP reachability via system `ping` |
| Alternate HTTP | `http` | GET a different URL (e.g. `/ready` when `/health` fails) |
| Shell command | `shell` | Run any command and capture output (`pg_isready`, `redis-cli ping`, etc.) |

**How diagnostics influence triage:**

- `cross:FAIL` (all endpoints down) → infrastructure outage, triage sets `should_act: false`, notification fired instead
- `dns:FAIL` or `tcp:FAIL` → network or process issue, not code; triage leans toward `should_act: false`
- `dns:ok`, `tcp:ok`, `cross` shows isolated failure → likely a code bug, agent investigates
- `tls:FAIL` → certificate expired or expiring; agent checks cert config

**Custom diagnostics example** — check database, cache, and an alternate health path:

```yaml
diagnostics:
  - check: dns
  - check: tcp
  - check: tls
  - check: cross
  - check: shell
    command: "pg_isready -h db.internal -p 5432"
  - check: shell
    command: "redis-cli -h redis.internal ping"
  - check: http
    url: "https://api.example.com/ready"
```

The coding agent is given the full diagnostic summary and explicit rules in its system prompt: it runs infrastructure checks first with `bash`, and only reads source code if the network and dependency checks come back clean.

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

```bash
docker run -d --name sidecar-db \
  -e POSTGRES_USER=sidecar \
  -e POSTGRES_PASSWORD=sidecar \
  -e POSTGRES_DB=sidecar \
  -p 5432:5432 \
  pgvector/pgvector:pg17
```

Or point `SIDECAR_DB_URL` at any PostgreSQL 15+ instance with pgvector (including [Neon](https://neon.tech) free tier).

Sidecar runs migrations automatically on `attach`.

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

  # Poll HTTP endpoints for uptime and latency
  - adapter: uptime
    poll_interval: "30s"
    uptime:
      endpoints:
        - url: "https://api.example.com/health"
          timeout: "5s"
          expect_status: 200
          # diagnostics run automatically on failure (dns, tcp, tls, cross)
          # add custom checks to troubleshoot dependencies:
          diagnostics:
            - check: dns
            - check: tcp
            - check: tls
            - check: cross          # are other endpoints also down?
            - check: shell
              command: "pg_isready -h db.internal -p 5432"
            - check: shell
              command: "redis-cli -h redis.internal ping"
            - check: http
              url: "https://api.example.com/ready"
        - url: "https://api.example.com/users"
          timeout: "3s"
          expect_status: 200
          expect_max_ms: 500    # fire if response takes >500ms

# How much autonomy Sidecar has per change type.
# Levels: auto-commit | pull-request | suggest-only | notify
autonomy:
  dependency_updates: auto-commit
  test_fixes: auto-commit
  bug_fixes: pull-request
  refactoring: suggest-only
  schema_changes: suggest-only
  log_fixes: suggest-only
  metric_fixes: suggest-only
  uptime_fixes: notify          # infra-class — alert a human, don't touch code

# Adversarial evaluator gate (defaults shown). Runs the tests over the diff
# before any auto-commit / pull-request and can REJECT (→ recorded as a
# suggestion). Fails closed. Default on.
verification:
  enabled: true

# Daily token budget. Sums provider-reported usage (input+output+cache)
# across triage + coding + evaluator, per workspace per UTC day; checked
# before triage. 0 = unlimited. Fails open on a metering error.
budget:
  daily_tokens: 0

# Skills: SKILL.md files in the target repo, loaded into the coding agent.
skills:
  dir: .sidecar/skills

# Model overrides (defaults shown)
models:
  coding: "anthropic/claude-sonnet-4-6"
  triage: "anthropic/claude-haiku-4-5-20251001"
  evaluator: ""           # optional; defaults to the coding model

# Embedding for persistent memory (optional but recommended)
embedding:
  provider: openai        # "openai" | "voyage"
  model: text-embedding-3-small

# Notifications — fired on task events (and for the `notify` autonomy level)
notifications:
  - provider: slack       # "slack" | "webhook" | "email"
    webhook: $SLACK_WEBHOOK_URL
    on: [completed, failed, notified]
```

### Autonomy Levels

| Level | Behaviour |
|-------|-----------|
| `auto-commit` | Evaluator-approved changes are committed on a `sidecar/<task-id>` branch |
| `pull-request` | Evaluator-approved changes are committed to a new branch and a PR is opened |
| `suggest-only` | Agent writes up what it would do; no code is changed, no evaluator runs |
| `notify` | No agent runs; a Slack / webhook / email notification fires (for infra-class signals) |

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

### `sidecar ask <question>`

Ask a question about the codebase, answered from the workspace's vector memory (no code is changed). Requires an `embedding` section in `sidecar.yaml` and a prior `sidecar attach` so memory exists to query.

```bash
sidecar ask "how does the auth middleware work?"
sidecar ask "what areas have been flagged as fragile?" --repo /path/to/project
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

## Memory System

When `embedding` is configured in `sidecar.yaml`, Sidecar maintains a persistent vector memory for each workspace:

- **After every task:** a Haiku reflect step extracts architectural facts (semantic), workflow patterns (procedural), and a task summary (episodic) from what the agent did, storing them as 1024-dimensional embeddings in PostgreSQL via pgvector.
- **Before every task:** the most relevant memory entries are retrieved by semantic similarity and injected into the agent's system prompt.
- **Scheduled sweeps:** a cron-triggered maintenance sweep prioritises fragile areas and under-tested code paths flagged in memory before falling back to generic checks.

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
| Uptime | `uptime` | HTTP endpoints that are down, return wrong status codes, or exceed a latency threshold |

## Demo Projects

Two minimal webapps in `examples/` are purpose-built to exercise every Sidecar signal adapter. Each ships with seeded bugs, intentionally failing tests, and an error-generating endpoint so you can see Sidecar react and fix things out of the box.

### Go demo (`examples/goapp`)

A Go task-tracker API on port 8080.

| Seeded issue | Adapter that catches it |
|---|---|
| `DELETE /tasks/{id}` returns 200 instead of 204 | CI adapter / on-demand task |
| `POST /tasks` accepts empty title | CI adapter / on-demand task |
| `POST /demo/stress` generates ERROR log bursts | Logs adapter |
| `HighErrorRate` Prometheus alerting rule | Metrics adapter |

```bash
# Start the webapp
cd examples/goapp
go run ./cmd/webapp          # listens on :8080

# Attach Sidecar (in another terminal)
export SIDECAR_DB_URL="postgres://sidecar:sidecar@localhost:5432/sidecar?sslmode=disable"
export ANTHROPIC_API_KEY="sk-ant-..."
sidecar attach .

# Trigger the logs adapter
for i in $(seq 1 6); do curl -s -X POST http://localhost:8080/demo/stress; done

# Run an on-demand task
sidecar task "fix the failing tests" --repo .
```

See `examples/goapp/README.md` for the full guide.

### Python demo (`examples/pyapp`)

A Flask task-tracker API on port 8081 with the same bugs and adapters as the Go demo.

| Seeded issue | Adapter that catches it |
|---|---|
| `DELETE /tasks/{id}` returns 200 instead of 204 | CI adapter / on-demand task |
| `POST /tasks` accepts empty title | CI adapter / on-demand task |
| `POST /demo/stress` generates ERROR log bursts | Logs adapter |

```bash
# Install and start
cd examples/pyapp
pip install -r requirements.txt
python app.py                # listens on :8081

# Attach Sidecar
export SIDECAR_DB_URL="postgres://sidecar:sidecar@localhost:5432/sidecar?sslmode=disable"
export ANTHROPIC_API_KEY="sk-ant-..."
sidecar attach .

# Run the tests (3 fail on the seeded bugs)
pytest test_app.py -v

# Let Sidecar fix them
sidecar task "fix the 3 failing pytest tests in test_app.py" --repo .
```

See `examples/pyapp/README.md` for the full guide.

## Architecture Notes

- Built on [harness](https://github.com/sausheong/harness) — a Go library for building LLM agents
- All state is in PostgreSQL; the daemon is stateless and restartable
- The `sidecar.yaml` in the target repo is the only coupling between the software and its agent — no code changes required in the managed project
- Each signal triggers an independent task that runs in its own isolated git worktree, so concurrent tasks never share a working tree
- Code-shipping changes pass an independent adversarial evaluator before they land; the gate fails closed (an unverifiable change is never committed)
- The coding agent has access to `read_file`, `write_file`, `edit_file`, and `bash` tools; write tools are gated by autonomy level. The evaluator gets only `read_file` and `bash` — it judges, it never edits
- A per-workspace daily token budget caps autonomous spend; usage is metered across triage, coding, and the evaluator
- Two-tier model split: a cheap triage model classifies every signal so always-on monitoring stays affordable; a stronger model does the coding
- Language-agnostic: Sidecar operates at the file, git, and shell level — any language works
