# Design: Remaining Loop-Engineering Parts — Skills, Budget Caps, Gate Test

**Date:** 2026-06-26
**Status:** Approved (design)
**Builds on:** `2026-06-26-loop-engineering-gaps-design.md` (evaluator + worktree)

## Motivation

After the evaluator + worktree branch, sidecar implements 4.5/5 loop-engineering
*moves* and 5/6 *parts*. Three gaps remain, mapped to the paper:

1. **Skills (the missing 6th part / intent debt).** `loop.BuildSystemPrompt` is a
   wall of instructions re-derived every run — the paper's "prompt pasted into a cron
   job nobody updates." Replace with a maintained, version-controlled skill set the
   loop reads from the target repo.
2. **Budget caps (token blowout, least-defended cost).** Only per-run `MaxTurns`
   exists; no daily ceiling. The evaluator now doubles agent runs on shipped changes,
   making a cap more relevant. The paper: "cap before you ship."
3. **Gate-routing test (verification debt in our OWN code).** The evaluator gate —
   the branch's central safety mechanism — is verified only by reading. Add an
   end-to-end test proving REJECT→suggested, PASS→committed, error→fail-closed.

## Scope

In: the three items. Out: cloud scheduling, MCP connectors, per-skill authoring tools.

---

## Item 1 — Skills (`internal/loop` + harness `tool/skills/disk`)

### Mechanism
Harness already provides everything: `tool/skills/disk.NewStore(root)` reads `SKILL.md`
files from a directory tree; `store.AsSkillProvider()` satisfies `runtime.SkillProvider`.
Passing it as `RuntimeDeps.Skills` (currently `RuntimeDeps{}` at `loop.go`) makes harness
(a) contribute a **skills index** to the static system prompt and (b) auto-register a
`load_skill` tool so the agent reads a skill's full body on demand.

This is **additive**: `BuildSystemPrompt` remains the per-signal framing (it must — the
git/CI/uptime instructions are signal-specific). Skills add persistent *project* knowledge
("what this service is, its conventions, fragile areas") that the loop otherwise re-derives.

### Location
Skills live at `.sidecar/skills/` in the **target repo** (the attached project), each as
`<name>/SKILL.md` per the harness disk-store layout. Path overridable via config:
```yaml
skills:
  dir: .sidecar/skills   # default; relative to repo root
```

### Loop wiring
- In `loop.New`, build the skills provider once: resolve `cfg.Skills.Dir` (default
  `.sidecar/skills`) against `repoPath`; if the dir exists, `skillsProvider =
  disk.NewStore(abs).AsSkillProvider()`, else nil. Store on `Loop`.
- In `Run`, pass `RuntimeDeps{Skills: l.skillsProvider}` for the **coding** runtime only
  (not triage — triage is a one-shot classifier with `MaxTurns:1`, no tool loop; not the
  evaluator — it judges, it doesn't need project lore).
- nil provider ⇒ harness behaves exactly as today (no index, no load_skill tool). So a
  repo without `.sidecar/skills/` is unaffected — zero behavior change.

### Config
```go
type SkillsConfig struct { Dir string `yaml:"dir"` }   // on Config
func (c *Config) SkillsDir() string                     // returns Dir or ".sidecar/skills"
```

### Why not replace BuildSystemPrompt
The signal framing (commit hash, CI URL, uptime diagnostics) is dynamic per-run and
cannot live in a static SKILL.md. Skills complement it. This matches the harness model
(skills index + load_skill *alongside* the system prompt) and the paper (skills pay off
intent debt; the per-turn instruction still exists).

## Item 2 — Budget caps (`internal/store` + `internal/loop` + `internal/config`)

### Meter
Tokens per workspace per UTC day. Token counts come from `AgentEvent.Usage` (populated on
`EventDone`): `InputTokens + OutputTokens` (cache fields ignored for the cap — they bill
differently but the cap is a coarse circuit breaker).

### Persistence
Token usage must survive restarts (a daily cap that resets on every daemon restart is no
cap). Record per task run via a new task event and aggregate by day:
- Reuse `task_events`: append `{type: "usage", payload: {input, output, total, model, role}}`
  after each runtime run (coding + evaluator + triage). One row per agent run.
- New store query: `SumWorkspaceTokensSince(ctx, workspaceID, since time.Time) (int, error)`
  — sums `payload->>'total'` across `task_events` joined to `tasks` for the workspace where
  `task_events.type='usage'` and `created_at >= since`. (Joins because `task_events` has no
  workspace_id; it has task_id → tasks.workspace_id.)

### Enforcement
- Config:
  ```yaml
  budget:
    daily_tokens: 0   # 0 = unlimited (default)
  ```
  ```go
  type BudgetConfig struct { DailyTokens int `yaml:"daily_tokens"` }
  func (c *Config) DailyTokenBudget() int   // 0 = unlimited
  ```
- In `Run`, **before triage** (the earliest LLM spend): if `DailyTokenBudget() > 0`, query
  `SumWorkspaceTokensSince(workspaceID, startOfUTCDay)`. If already `>=` budget:
  - Append `task_event{type:"budget_exceeded", payload:{spent, budget}}`, set status
    `StatusSkipped` (reuse existing constant), fire a new `notify.EventBudgetExceeded`,
    return nil. No agent runs.
- Token accounting helper: a small `usageRecorder` accumulates `*llm.Usage` from the event
  loops and writes the `usage` event. Applied to all three runtimes (triage, coding,
  evaluator) so the meter reflects total spend.

### Why before triage
Triage itself costs tokens (haiku, but non-zero). Checking first means an exhausted budget
spends nothing. The cap is a pre-run gate, matching the paper's "cap *before* you ship."

### New constants / events
- `notify.EventBudgetExceeded` (+ dispatch wiring like existing events).
- No new task status — reuse `StatusSkipped` with a distinguishing `budget_exceeded` event.

## Item 3 — Gate-routing integration test (`internal/loop`)

### Convention
Mirror the repo's existing integration tests: `//go:build integration` + skip when
`SIDECAR_TEST_DB_URL` unset (same as `internal/store/store_test.go`). Runs only under
`go test -tags integration`, not the default hermetic suite.

### Scripted provider
A test `llm.LLMProvider` built on harness `llmtest.Base` (provides Models/NormalizeToolSchema
boilerplate). It implements `ChatStream` to return canned responses keyed by the agent's
system prompt or model role, so one provider can serve triage + coding + evaluator within a
single `Run`:
- Triage turn → emit JSON `{"should_act":true,"change_type":"bug_fix","reason":"..."}`.
- Coding turn → emit a tool call to write a file (so there's a diff to evaluate), then stop.
- Evaluator turn → emit `{"pass": <param>, "reasons":"..."}` — the test toggles pass.

Config for the test sets `bug_fixes: auto-commit` and `verification.enabled: true`.

### Cases
1. **PASS → committed:** evaluator returns pass=true ⇒ task `completed`, a `sidecar/<id>`
   branch exists with the change, an `evaluation` event with pass=true.
2. **REJECT → suggested, no commit:** evaluator returns pass=false ⇒ task `suggested`, NO
   `sidecar/<id>` branch (or branch has no commit), `suggestion` event recorded.
3. **Evaluator error → fail-closed:** scripted provider errors on the evaluator turn ⇒ task
   `suggested` (not committed), proving fail-closed end-to-end.

Each case: fresh temp git repo (mirror `output_test.go` `initRepo`), real DB via
`SIDECAR_TEST_DB_URL`, run `Loop.Run` with a synthetic git-commit signal, assert task status
+ task events + git branch state.

### Note on token budget
A 4th integration case asserts budget enforcement: set `daily_tokens` very low, pre-seed a
`usage` event over budget, run, assert `budget_exceeded` event + `skipped` status + no agent
spend.

## Error handling

- Skills dir missing/unreadable → nil provider, log debug, proceed (no skills). Never fail
  a run because skills are absent.
- Skills store construction error → log warn, nil provider, proceed.
- Budget query error → log warn, **fail open** (allow the run). A metering glitch must not
  halt maintenance; the cap is a cost guard, not a safety gate (unlike the evaluator, which
  fails closed). This asymmetry is deliberate and documented.
- Usage event write failure → log warn, don't fail the task (accounting is best-effort;
  worst case the cap undercounts).

## Testing

| Unit | Test | Suite |
|------|------|-------|
| `config.SkillsDir` / `DailyTokenBudget` defaults | pure | hermetic |
| `store.SumWorkspaceTokensSince` | real DB | integration |
| `loop` skills provider nil when dir absent | pure-ish (temp dir) | hermetic |
| `loop.Run` gate routing (PASS/REJECT/error) | scripted provider + real DB | integration |
| `loop.Run` budget gate | scripted provider + real DB | integration |

## Files

- New: `internal/loop/run_integration_test.go`, `internal/loop/skills.go` (provider build),
  this design doc.
- Modified: `internal/config/config.go` (+SkillsConfig, BudgetConfig), `internal/loop/loop.go`
  (skills wiring, budget gate, usage recording), `internal/store/task.go` (SumWorkspaceTokensSince),
  `internal/notify/notify.go` (EventBudgetExceeded), `sidecar.yaml` + `CLAUDE.md` (docs).
- Test infra: a scripted `llm.LLMProvider` (in the integration test file).
