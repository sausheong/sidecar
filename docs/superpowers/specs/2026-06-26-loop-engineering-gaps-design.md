# Design: Closing the Loop-Engineering Gaps

**Date:** 2026-06-26
**Status:** Approved (design)
**Author:** sidecar maintainers

## Motivation

Analysed against *Loop Engineering: The Anthropic Playbook* (Osmani/Steinberger/Cherny,
June 2026), sidecar is a faithful layer-4 loop: it implements Discovery (adapters +
triage), Persistence (Postgres + pgvector + git + notifications), and Scheduling
(daemon + cron) well. Two of the paper's five **moves** are weak, and two unrelated
code-health issues block work:

1. **Verification (the paper's hardest move) is missing for shipped code.** The coding
   agent grades its own work — the "Nodding Loop" anti-pattern. There is no independent
   evaluator that can say "no" before an `auto-commit`. The paper's remedy is a
   generator/evaluator split: a *separate* agent, fresh context, "assume broken", that
   **acts** (runs tests) rather than reads.
2. **Handoff has no isolation (latent "Tangled Loop").** The coding agent mutates the
   checked-out repo directly. Safe only because the daemon processes signals serially;
   any future concurrency corrupts shared working state.
3. **Broken build.** Harness added `google.golang.org/genai`; sidecar's `go.sum` was
   never updated, so every package importing `harness/llm` fails to compile.
4. **Dead config + secrets risk.** `uptime_fix` is handled in `ResolveAutonomy` but
   omitted from the triage prompt's `change_type` enum, so the model never emits it.
   `.gitignore` is empty next to an untracked `.env` holding real API keys and a 22 MB
   binary.

## Scope

In: all four items above. Out: cloud scheduling (Table IV), MCP-based connectors,
budget caps (token blowout is partially mitigated by `MaxTurns`; out of scope here).

---

## Item 1 — Fix broken build (mechanical)

`go mod tidy` to add the missing `genai` `go.sum` entry. Gate: `go build ./...` and
`go vet ./...` clean. **Must land first** — nothing else compiles or tests without it.

## Item 2 — Secrets + dead config (mechanical)

- Add `.gitignore` scoped to known artifacts: `.env`, `/sidecar` (the built binary).
  No blanket `*.pdf`/`*` rules.
- Add `"uptime_fix"` to the enumerated `change_type` list in `triageSystemPrompt`
  (`internal/triage/triage.go`). `ResolveAutonomy` already maps it.

## Item 3 — Adversarial evaluator (`internal/evaluate`)

A new package implementing the generator/evaluator split.

### Why not `runtime.Review`
`harness`'s `Review` restricts the reviewer to **write-only** tools and **snapshots the
generator's conversation**. Both are wrong here: the paper requires the evaluator to
*act* (read diff, run tests) and to carry *none* of the generator's self-persuasion. So
the evaluator is a **fresh `runtime.Runtime`**, not `Review`.

### Interface
```go
package evaluate

type Verdict struct {
    Pass    bool
    Reasons string // populated on REJECT; may be set on PASS as notes
}

// Evaluate builds a fresh skeptic runtime over the diff and returns a verdict.
// workDir is the worktree (or repo) containing the generator's uncommitted changes.
func Evaluate(ctx context.Context, provider llm.LLMProvider, model, workDir, taskSummary string) (Verdict, error)
```

- **Tools:** `file.ReadFileTool` + `bash.BashTool` (read + execute). **No** write/edit —
  the evaluator judges, it does not fix.
- **Input:** the evaluator's first user message embeds `git diff` (HEAD vs working tree)
  of the generator's changes plus `taskSummary`. Fresh session; no parent history.
- **System prompt** (paper's stance): "You are an adversarial code reviewer. ASSUME this
  diff is BROKEN until proven otherwise. Do NOT praise. Run the tests. Check edge cases
  the author skipped. Verify behaviour matches the task. Respond with ONLY JSON:
  `{\"pass\": false, \"reasons\": \"...\"}`. PASS only if every check holds."
- **Parsing:** reuse the tolerant first-`{`/last-`}` JSON extraction pattern from
  `triage.ParseTriageResponse`. On parse failure → conservative `Verdict{Pass: false,
  Reasons: "evaluator response unparseable"}` (fail closed: an unverifiable change is
  not shipped).
- **Limits:** `MaxTurns` ~8 (needs multiple bash calls to run tests), `Timeout` via ctx.

### Model
Default = coding model. Override via `models.evaluator` config (the lever is
fresh-context + skeptic stance, not necessarily a different model — but config allows it).

## Item 4 — Worktree-isolated handoff (`internal/worktree`)

### Insight
Today both `auto-commit` and `pull-request` produce a committed `sidecar/<id>` branch
*without* leaving changes on the user's branch (`CommitBranch` does
`checkout -B … ; add ; commit ; checkout -`). A worktree yields an **identical outcome**
with no shared-state risk.

### Interface
```go
package worktree

type Worktree struct {
    Path   string // isolated working dir
    Branch string // sidecar/<taskID>
}

// Create runs `git worktree add <tmp> -b sidecar/<taskID>` off current HEAD.
func Create(repoPath, taskID string) (*Worktree, func() error, error) // returns wt, cleanup, err
```

- The coding agent **and** evaluator run with `WorkDir = wt.Path`.
- Commit happens *in* the worktree (already on the branch) → `output.CommitBranch`
  simplifies to `add -A; commit` (no checkout dance) when given an already-isolated dir.
  New method `CommitInPlace(message)` for the worktree path; keep `CommitBranch` for the
  on-demand/non-worktree CLI path to avoid breaking `internal/cli`.
- `cleanup()` → `git worktree remove --force`. Branch persists in shared `.git`, so PR
  creation is unaffected.
- Worktree created only for code-shipping autonomy levels; `suggest-only`/`notify` skip it.

## Loop integration (`internal/loop/loop.go`)

New flow for code-shipping levels (`auto-commit`, `pull-request`):

```
triage → (memory) → create worktree → run coding agent in worktree
  → if changes & verification.enabled: evaluate(worktree)
       REJECT → record `evaluation` event; downgrade to suggestion;
                status=suggested; EventSuggested; cleanup; return
       PASS   → record `evaluation` event (pass)
  → commit in worktree → (PR create) → status=completed → cleanup
```

`suggest-only` and `notify` paths are unchanged (no worktree, no evaluator).

### Config additions (`internal/config`)
```yaml
verification:
  enabled: true        # default true — closes the Nodding Loop
models:
  evaluator: ""        # optional; defaults to coding model
```
`enabled` defaults to **true** (a default-off gate wouldn't close the gap). The evaluator
gates **both** `auto-commit` and `pull-request`: a REJECT on a PR-bound change becomes a
suggestion rather than an unreviewed merge-ready PR.

### New status / event
- Reuse existing `StatusSuggested` for REJECT downgrades.
- New task event kind `"evaluation"` with `{pass, reasons, model}`.

## Error handling

- Evaluator build/run error (not a REJECT verdict): log warn, **fail closed** — treat as
  REJECT-equivalent (downgrade to suggestion). An unverifiable change must not auto-commit.
- Worktree create failure: log, fall back to in-repo execution (current behaviour) so a
  git-version issue doesn't take the loop down — but record the degradation in a task event.
- Worktree cleanup failure: log warn only; never fail the task on cleanup.

## Testing (TDD)

| Unit | Test |
|------|------|
| `evaluate.parseVerdict` | PASS/REJECT/fenced-JSON/garbage→fail-closed |
| `evaluate.buildPrompt` | diff + summary embedded; skeptic stance present |
| `worktree.Create/cleanup` | real-git: isolated dir, branch exists after cleanup, dir gone (mirrors existing `output` git tests) |
| `loop` gate routing | REJECT→suggested+no commit; PASS→completed+commit; evaluator error→fail-closed (table test with a fake provider) |
| `triage` prompt | asserts `change_type` enum contains `uptime_fix` (and the others) |
| build | `go build ./...` clean |

## Files

- New: `internal/evaluate/{evaluate.go,evaluate_test.go}`,
  `internal/worktree/{worktree.go,worktree_test.go}`, `.gitignore`,
  this design doc.
- Modified: `internal/loop/loop.go`, `internal/config/config.go`,
  `internal/triage/triage.go`, `internal/output/output.go`, `go.mod`, `go.sum`.
