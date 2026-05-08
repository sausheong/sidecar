# Sidecar — Design Spec

**Date:** 2026-05-08
**Status:** Approved

---

## 1. Vision

Sidecar is an autonomous engineering agent that attaches to any software project as a persistent sidecar process and continuously improves, updates, patches, and maintains that software — without being embedded inside it.

It is not a chatbot, a workflow runner, or an IDE assistant. It is a long-running operator that watches a codebase, reacts to problems, and proactively improves quality when idle.

Built on [Harness](https://github.com/sausheong/harness) in Go.

---

## 2. Core Properties

| Property | Decision |
|----------|----------|
| Attachment model | Sidecar — separate process, attaches from outside |
| Integration | Config-driven adapter model (`sidecar.yaml` in target repo) |
| Triggers | All four: continuous monitoring, event-driven, scheduled, on-demand |
| Default posture | Reactive to signals; proactive when idle |
| Autonomy | Configurable per project and per change type |
| Target software | Language-agnostic (operates on files, git, CI, shell) |
| Memory | Persistent — accumulates architectural knowledge over time |
| Persistence | PostgreSQL + pgvector |

---

## 3. Architecture

```
Target Repository
  └── sidecar.yaml       ← config: signals, autonomy policies, scope
  └── source code, tests, CI config

Signal Sources (via Adapters)
  ├── git        → commits, branches, diffs
  ├── github-ci  → GitHub Actions webhooks (failure, flake)
  ├── logs       → local log file tailing
  ├── metrics    → Datadog, Prometheus, alerts
  └── schedule   → cron-driven proactive cycles
                        │
                        ▼
┌─────────────────────────────────────────────────┐
│            Sidecar Daemon                       │
│                                                 │
│  Signal Router → Triage → Improvement Loop      │
│                  (Harness Runtime)              │
│                                                 │
│  Workspace Memory (PostgreSQL + pgvector)       │
└─────────────────────────────────────────────────┘
                        │
                        ▼
Change Output (per autonomy policy in sidecar.yaml)
  ├── auto-commit   → pushes directly to branch
  ├── pull-request  → opens PR for human review
  └── suggest-only  → posts comment or opens issue
```

The daemon is started with `sidecar attach .` from the target repo root. It reads `sidecar.yaml`, connects the declared adapters, and begins listening.

---

## 4. sidecar.yaml

Placed in the target repo root. The only coupling between the software and its agent.

```yaml
# Workspace identity
workspace:
  name: payment-service
  language: go          # advisory hint only — agent adapts regardless

# Signal adapters — what the agent watches
signals:
  - adapter: git
    watch: [push, pr]
  - adapter: github-ci
    watch: [failure, flake]
  - adapter: schedule
    cron: "0 2 * * *"   # nightly proactive sweep

# Autonomy policy — per change type
autonomy:
  dependency_updates: auto-commit
  test_fixes:         auto-commit
  bug_fixes:          pull-request
  refactoring:        suggest-only
  schema_changes:     suggest-only

# Model routing (optional — defaults to sensible models if omitted)
models:
  planning: anthropic/claude-sonnet-4-6
  coding:   anthropic/claude-sonnet-4-6
  triage:   anthropic/claude-haiku-4-5

# File scope (optional — defaults to all files)
scope:
  include: [src/, tests/, go.mod]
  exclude: [secrets/, migrations/]
```

### Autonomy levels

| Level | Meaning |
|-------|---------|
| `auto-commit` | Agent pushes to a dedicated `sidecar/<task-id>` branch, then auto-merges if CI passes |
| `pull-request` | Agent opens a PR; human reviews and merges |
| `suggest-only` | Agent posts a comment or opens an issue |

### Planned adapters

| Adapter | Signal source | Phase |
|---------|--------------|-------|
| `git` | Local repo events (push, PR, commit) | 1 |
| `schedule` | Cron-driven proactive sweeps | 1 |
| `github-ci` | GitHub Actions webhook (failure, flake) | 2 |
| `logs` | Local log file tailing | 5 |
| `metrics` | Datadog, Prometheus alerts | 5 |

---

## 5. Improvement Loop

Every signal — regardless of source — flows through the same loop:

```
① OBSERVE
   Adapter delivers SignalEvent{type, source, payload}

② TRIAGE  (haiku — cheap, fast)
   Classify the signal type
   Check autonomy policy: what action is permitted?
   Check episodic memory: seen this before?
   → TriageResult{class, allowed_action, priority}

③ PLAN  (sonnet — capable)
   Retrieve relevant context from workspace memory (semantic search)
   Decompose into subtasks
   → TaskPlan{steps[], estimated_risk, files_affected}

④ EXECUTE  (sonnet)
   Read files → write patches → run tests → verify
   Tools: filesystem, bash (test runner), git
   On failure: retry up to N times, then escalate to next autonomy level

⑤ OUTPUT
   auto-commit   → git commit + push to branch
   pull-request  → open PR with generated summary
   suggest-only  → post comment / open issue

⑥ REFLECT
   Store outcome in workspace memory
   Update knowledge: what worked, what didn't
   Tag fragile areas for future proactive attention
```

**Three execution paths share this loop:**

- **Reactive** — signal arrives → full loop → output (~minutes per task)
- **Proactive** — cron fires → sweep for improvement opportunities → batch execute
- **On-demand** — `sidecar task "..."` → enters loop at PLAN, skipping triage

**Cost design:** Triage uses a cheap fast model so always-on monitoring stays affordable. The capable model only fires when real work is required.

---

## 6. Workspace Memory

Four memory categories stored in PostgreSQL, semantic entries indexed with pgvector:

| Category | Content | Used at |
|----------|---------|---------|
| **Episodic** | Task history — what was done, when, outcome | Triage: "have I seen this?" |
| **Semantic** | Architecture understanding — patterns, fragile areas, conventions | Plan: context retrieval |
| **Procedural** | Learned workflows — how to run tests, what tools to use | Execute: correct tooling |
| **Policy** | Constraints from `sidecar.yaml` + feedback from PR reviews | Triage: before any action |

### PostgreSQL schema

```sql
workspaces    (id, name, path, config_hash, created_at)
tasks         (id, workspace_id, signal_type, status, summary, created_at)
task_events   (id, task_id, type, payload, created_at)
memory_entries(id, workspace_id, category, content, embedding vector(1536))
policies      (id, workspace_id, rule, source)  -- source: yaml | learned
```

Semantic memory is retrieved via pgvector cosine similarity at Plan time — the agent pulls the most relevant architectural facts into its context rather than scanning the whole codebase on every task.

**Long-term payoff:** after weeks of operation the agent accumulates codebase-specific knowledge (fragile areas, team style preferences, correct test invocation) that makes it faster and more accurate than starting fresh on each task.

---

## 7. CLI Commands

```
sidecar attach .                    # attach to current repo, start daemon
sidecar daemon                      # start runtime (called by attach)
sidecar task "fix flaky auth test"  # submit on-demand task
sidecar ask "how does auth work"    # query workspace memory
sidecar status                      # show running tasks and recent history
sidecar stop                        # stop the daemon
```

---

## 8. Implementation Phases

| Phase | Deliverables |
|-------|-------------|
| **1 — Core** | CLI (`attach`, `task`, `status`), git adapter, schedule adapter, filesystem + bash + git tools, Harness runtime integration, PostgreSQL schema |
| **2 — Reactive** | CI adapter (GitHub Actions webhook), triage loop, autonomy policy enforcement, PR creation |
| **3 — Memory** | pgvector integration, all four memory categories, semantic retrieval at plan time, reflect step |
| **4 — Proactive** | Idle sweep (stale deps, dead code, missing tests), on-demand `sidecar task`, `sidecar ask` |
| **5 — Adapters** | Logs adapter, metrics adapter (Datadog/Prometheus), additional CI providers |

---

## 9. Non-Goals

- Hosted/SaaS operation — Sidecar is local-first
- Visual workflow builder or drag-and-drop orchestration
- Conversational chat interface
- Language-specific deep analysis (operates at file/git/shell level only)
