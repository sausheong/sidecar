# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Is Sidecar

Sidecar is an autonomous engineering agent that attaches to any software project as a persistent sidecar process and continuously maintains it — fixing bugs, updating dependencies, and proactively improving code quality.

It is language-agnostic (operates at file/git/CI/shell level), built on [Harness](https://github.com/sausheong/harness), and configured via a `sidecar.yaml` file placed in the target repository.

Design spec: `docs/superpowers/specs/2026-05-08-sidecar-design.md`

## Tech Stack

- **Go 1.25+** — primary language
- **Module:** `github.com/sausheong/sidecar`
- **Harness** (`github.com/sausheong/harness` via local replace `../harness`) — LLM agent loop, tool invocation, providers
- **PostgreSQL** — workspaces, tasks, task_events (via `pgx/v5`)
- **robfig/cron/v3** — schedule adapter
- **cobra** — CLI

## Standard Go Commands

```bash
go build ./...
go test ./...                                                    # unit tests (hermetic)
go test -tags integration ./internal/store/...                   # requires SIDECAR_TEST_DB_URL
go test -tags integration ./internal/loop/...                    # gate-routing + budget (needs SIDECAR_TEST_DB_URL)
go build -o /tmp/sidecar ./cmd/sidecar                           # build the binary
```

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `SIDECAR_DB_URL` | yes | PostgreSQL DSN, e.g. `postgres://localhost/sidecar` |
| `ANTHROPIC_API_KEY` | yes | Anthropic API key for LLM agent |

## CLI Commands (Phase 1)

```
sidecar attach [path]           # attach to repo, start daemon (reads sidecar.yaml)
sidecar task "description"      # submit on-demand task (--repo flag for non-CWD)
sidecar status                  # show recent tasks (--repo flag for non-CWD)
```

## Package Structure

```
cmd/sidecar/          CLI entrypoint
internal/
  cli/                cobra commands (attach, task, status) + buildAdapters()
  config/             sidecar.yaml → Config struct; Load(), ValidAutonomyLevel()
  store/              PostgreSQL: Connect, Migrate, Workspace/Task CRUD, ErrNotFound
  adapter/            Adapter interface, SignalType constants
  adapter/git/        Polls git log for new commits (GitAdapter)
  adapter/schedule/   Cron-driven signals (ScheduleAdapter, robfig/cron)
  output/             CommitBranch() — creates sidecar/<task-id> branch
  loop/               Improvement loop: wraps Harness runtime, BuildSystemPrompt, ResolveModels
  daemon/             Daemon — fans in adapter signals, routes to loop.Run handler
```

## Key Design Decisions

- **sidecar.yaml** lives in the target repo and is the only coupling between that software and its agent
- **Triage uses haiku** (cheap/fast) — always-on monitoring stays affordable; coding uses sonnet
- **Autonomy is per-change-type**: `auto-commit` / `pull-request` / `suggest-only` configured in `sidecar.yaml`
- **Git adapter** records HEAD at startup — pre-existing commits never trigger signals
- **Adversarial evaluator** — code-shipping changes (`auto-commit`/`pull-request`) are gated by a fresh skeptic runtime (`internal/evaluate`) that runs tests over the diff and can REJECT (downgrades to a suggestion). Default on via `verification.enabled`.
- **Worktree isolation** — each code-shipping task runs in its own `git worktree` (`internal/worktree`), so concurrent signals never share a working tree.
- **Skills** — the coding agent loads `SKILL.md` files from the target repo's `.sidecar/skills/` (configurable via `skills.dir`) through harness's skill provider; absent dir ⇒ no change. Pays off the paper's "intent debt".
- **Budget caps** — `budget.daily_tokens` enforces a per-workspace per-UTC-day token ceiling (input+output, summed from `usage` task events) checked before triage; fails open on metering error. The paper's "cap before you ship". Caveat: this is a coarse circuit breaker, not precise accounting — harness's `AgentEvent` reports token usage for only the FINAL turn of a multi-turn run, so the meter undercounts real spend on multi-turn coding runs. Set the cap conservatively.
- **Loop status constants** are in `internal/loop`: `StatusPending`, `StatusRunning`, `StatusCompleted`, `StatusFailed`
- **`store.ErrNotFound`** — defined in `internal/store/db.go`; used by `GetWorkspaceByPath` for missing workspaces

## Implementation Status

| Phase | Status | Deliverables |
|-------|--------|-------------|
| **1 — Core Runtime** | ✅ Complete | CLI, git + schedule adapters, Harness loop, PostgreSQL store |
| **2 — Reactive** | ✅ Complete | CI adapter (GitHub Actions), triage loop, PR creation; adversarial evaluator gate (`internal/evaluate`) + worktree isolation (`internal/worktree`) via `verification.enabled` |
| **3 — Memory** | ✅ Complete (harness v0.2.0) | pgvector storage; agent-driven writes via harness MemoryTool + HarnessStoreAdapter; runtime.Review fires on LifecycleHooks.OnStop |
| **4 — Proactive** | Pending | Idle sweeps, `sidecar ask` |
| **5 — Adapters** | Pending | Logs, metrics, additional CI providers |

## Key Reference

- Harness developer docs: `../harness/developer.md`
- Design spec: `docs/superpowers/specs/2026-05-08-sidecar-design.md`
- Phase 1 implementation plan: `docs/superpowers/plans/2026-05-08-sidecar-phase-1.md`
