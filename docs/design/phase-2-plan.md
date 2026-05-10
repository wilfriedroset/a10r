# Phase 2 — pre-launch feature batch (a10r)

> **Internal artefact.** Like the original feature-batch plan, this
> file is project-internal. Phase 6 of the launch plan should
> drop it before the v0.0.1 history rewrite ships.

## Context

Phase 1 closed the 11-item ASAP punch list from the 2026-05-08
triage (commits `3cdfb37 … ba7dee3`) plus the QA-driven follow-up
fix `ba7dee3`. Phase 2 stays pre-launch — every accepted
candidate becomes a v0.0.1 blocker — and pulls 15 items across
two pillars:

1. **Phase 1 follow-ons** — work the phase-1 commits explicitly
   deferred. Closing these completes the per-page consistency
   story and the headless-CLI parity that motivated phase 1.
2. **Selected DEFER candidates** from the same scout doc, picked
   for high-leverage / low-risk slots that round out the launch
   surface.

Same shape as phase 1: TDD, `prek -a` clean, subagent review
loop, one commit per logical unit, conventional commit messages,
ADRs only on the strict bar (hard-to-reverse + surprising +
real trade-off).

## Context-window discipline (load-bearing)

Phase 1 was implemented by a single agent driving every item
end-to-end in one session, and the resulting context pressure
visibly degraded the last few commits — D4 / D2 shipped on the
alerts page only, A1 doctor lost three checks, A3 lost
`--one-shot --kv`. Phase 2 closes that loop precisely BECAUSE
phase 1 ran out of room. **The resuming agent must not repeat
that pattern.**

The resuming agent is an **orchestrator**, not the implementer:

1. Read this plan once. Pick a Wave 1 item.
2. **Delegate that single item** to a fresh sub-agent (Agent
   tool, `general-purpose` or `Plan` subtype as appropriate)
   with a self-contained prompt — see the prompt template at
   the bottom of this file.
3. The sub-agent returns a single message: either committed
   work (when given write authority) or a tight summary +
   findings.
4. The orchestrator runs `go test ./... && prek -a`, kicks the
   review-loop sub-agent, lands the commit, then picks the next
   item. **The orchestrator does not implement.**

Why this matters:
- Each sub-agent gets a focused, self-contained brief. Its
  context fills with the files it needs and nothing else.
- The orchestrator's context stays small: this plan, recent
  test/prek output, the pending task list. No 50k-token files
  loaded "just in case".
- Failure mode mitigated: if a sub-agent hits its own ceiling,
  the orchestrator can re-spawn or split the work. The
  orchestrator never inherits that pressure.

**Rule of thumb:** if the orchestrator finds itself reading
implementation files, that's the signal to stop and delegate.
The plan items below are intentionally narrow so each
delegation prompt fits in well under 5k tokens.

---

## Wave 1 — independent (9 commits)

These don't depend on phase 1 commits beyond what already
landed; each can land in any order.

### P2.W1.1 — G3 user aliases (`<config-dir>/aliases.yaml`)

**Source:** scout doc §G3.

**Files:**
- `internal/tui/cmdbar/cmdbar.go` — extend `Resolver` to load
  `<config-dir>/aliases.yaml` at startup; merge user entries
  onto the built-in alias map. Conflicts (same short bound to
  user value AND built-in) fail closed with a precise error,
  same approach as C3 keybinding conflicts.
- `internal/config/aliases.go` (new) — Aliases YAML schema +
  loader. Single map shape: `{<short>: <expanded command>}`.
- `internal/config/aliases_test.go` — round-trip + conflict
  detection.
- `cmd/info.go` — surface the resolved alias count in `a10r
  info` so operators can confirm the file landed.
- `docs/end-users/configuration.md` — Aliases section.

**Effort:** S (~half day).

### P2.W1.2 — G5 raw YAML toggle on detail pages (`y`)

**Source:** scout doc §G5.

**Files:**
- `internal/tui/page/alert/alert.go` — add `y` keybinding;
  togglable view between the structured render and the raw
  alert payload as YAML (use `output.WriteYAML` to a buffer).
- `internal/tui/page/silences/silence_detail.go` (or current
  silence-detail surface) — same toggle.
- Bindings entries advertising the new key.

**Effort:** S (~hour). Reuses existing viewport.

### P2.W1.3 — E1 mouse-wheel scroll

**Source:** scout doc §E1.

**Files:**
- `internal/tui/app/app.go` — enable `tea.MouseModeCellMotion`
  in `Init`. Route wheel events on table pages to the existing
  cursor-walk handlers; route in the help modal to a viewport
  scroll. Explicitly NOT click-to-focus (keyboard-first
  contract).
- Tests on the wheel-routing dispatcher.

**Effort:** S (~few hours, ~30 LOC scope per scout).

### P2.W1.4 — F4 search-mode autodetect on `/` filter

**Source:** scout doc §F4.

**Files:**
- `internal/tui/footer/prompt.go` (or wherever the live `/`
  filter parses) — implement the lfk four-mode detection:
  `~`-prefix → fuzzy, `\`-prefix → literal, regex-meta-only
  detection (require ≥2 metas to flip — single `.` stays
  substring), default substring.
- Tests for each mode + the substring-vs-regex boundary.
- `docs/end-users/keybindings.md` — document the rule next to
  the existing filter section.

**Effort:** S (~half day).

### P2.W1.5 — C3 user-overridable keybindings

**Source:** scout doc §C3. Unblocks `docs/design/open-questions.md` deferred item.

**Files:**
- `internal/config/keys.go` (new) — schema:
  `<config-dir>/keys/<profile>.yaml` listing
  `<action>: [keys...]`. Profile loader.
- `internal/tui/keys/dispatch.go` — accept user overrides on
  top of the action-registry defaults. Reserved keys (0-9
  tenant quick switch per the open question) are NOT
  overridable; refuse to start with a precise error if a user
  file binds them.
- Conflict detection (same key bound to two enabled actions in
  the user file) fails closed with file:line.
- Tests across happy / conflict / reserved-key / unknown-action
  scenarios.

**ADR:** Yes — `docs/adr/0010-user-keybindings-schema.md`.
Schema is hard-to-reverse (user files in the wild), the
reserved-key rule is surprising without context, and the choice
between "shadow defaults" vs "replace defaults" is a real
trade-off.

**Effort:** M (~1.5 days).

### P2.W1.6 — A9 config-dir merge

**Source:** scout doc §A9.

**Files:**
- `internal/config/loader.go` — extend `Load` to merge
  `<config-dir>/config.d/*.yaml` (recursive, symlinks
  followed, lexical order) onto the base file. Last-key-wins
  for scalars; backends de-duplicated by name with a precise
  "duplicate name" error on conflict (fail-closed per the
  open-questions decision).
- Tests on merge order, conflict detection, symlink follow.
- `docs/end-users/configuration.md` — config.d/ section.

**Effort:** S (~half day).

### P2.W1.7 — E8 hint bar (config-gated)

**Source:** scout doc §E8. Per the no-features-without-go memory
on optional-via-config: gated behind `tips: true` (default
**off** to honour the prior feedback that scouted features stay
opt-in).

**Files:**
- `internal/tui/footer/hintbar.go` (new) — one-line band that
  rotates a curated tip from `internal/tui/help/tips.go` on a
  configurable cadence.
- `internal/config/types.go` — `tips: bool` field under
  `defaults` or a new `tui:` block.
- Tests on rotation cadence + opt-out short-circuit.

**Effort:** S (~half day).

### P2.W1.8 — G4 filter suggestions (recent-filter ring)

**Source:** scout doc §G4.

**Files:**
- `internal/tui/footer/filter_history.go` (new) — separate
  rings per matcher class:
  `cmd-history` (`:` aliases share with G3),
  `filter-history` (`/` substring/regex/fuzzy),
  `silence-matcher-history` (silences page Prom-matcher input).
  Plain text under `$XDG_STATE_HOME/a10r/`, 0o600, last-N capped.
- Hook into the prompt to cycle on Tab / arrow keys.

**Effort:** S (~half day).

### P2.W1.9 — B5 smart column-width allocation

**Source:** scout doc §B5.

**Files:**
- `internal/tui/page/format/columns.go` (new) — duf-style
  weight-driven distributor: measure content per column, reserve
  fixed widths for narrow columns, distribute remainder by
  per-column weights.
- `internal/tui/page/alerts/render.go` — use the distributor
  for the alerts table (labels are the unbounded column; rest
  shrinks proportionally).
- Tests on weight distribution + ellipsis truncation.

**Effort:** M (~1 day).

---

## Wave 2 — composes on phase 1 commits (6 commits)

### P2.W2.1 — D4 watch toggle on silences / groups / receivers

**Phase 1 reference:** `34b3a88 feat(alerts): watch-mode toggle`.

The alerts page is the canonical reference; lift the same shape
mechanically:
- `paused bool` + `pausedRefresh bool` fields
- `case poll.DataMsg: if p.paused && !p.pausedRefresh { return }`
- `case "w":` toggle handler
- Footer prepends `WATCH OFF` (concatenated with `refreshing…`
  when both)
- `requestRefresh` sets `pausedRefresh = true` when paused
- Bindings entry

**Files:** `internal/tui/page/{silences,groups,receivers}/`
(handlers + render + tests).

**Effort:** S (~half day; copy-paste once the first page is
done).

### P2.W2.2 — D2 error band on silences / groups / receivers

**Phase 1 reference:** `6fe64ba feat(alerts): error band`.

Same lift pattern:
- `lastErrors map[string]string` field
- `case poll.BackendStatusMsg:` handler with delete-on-empty
- `ErrorBand()` method (in-scope filter, sort, single-vs-multi
  formatting)
- `renderErrorBand()` in View prepended above the table

**Files:** `internal/tui/page/{silences,groups,receivers}/`.

**Effort:** S-M (~1 day).

### P2.W2.3 — A1 doctor: TLS-expiry / capabilities / clock-skew

**Phase 1 reference:** `ce1b375 feat(doctor)`. The phase-1 commit
deferred these three checks for context budget reasons; each
needs additional infrastructure.

**Files:**
- `internal/doctor/checks.go` — three new checkers:
  - `TLSExpiryChecker`: opens a TLS connection to the URL host
    via a tiny standalone helper (`internal/backend/tls/probe.go`,
    new), inspects `cert.NotAfter`. <30 days → Warning; expired
    → Error. Does NOT reach into the constructed http.Client
    (avoids transport-internal exposure).
  - `CapabilitiesChecker`: for each enabled
    `config.Capabilities` flag, attempt the corresponding API
    call once. Mismatch (e.g. ConfigAPI enabled but vanilla AM
    404s) → Error. Per-cap probe map.
  - `ClockSkewChecker`: compare server `Date` header from
    `/api/v2/status` response vs local clock; >30s warn.
    Implements `ProbeReadyAt(ctx) (time.Time, error)` on
    backend.Client (new method) so the test fake can pin the
    timestamp.
- Per-check tests with table-driven scenarios.

**ADR:** Optional — probably skip. The TLS helper, the
capabilities probe map, and the clock-skew threshold are all
straightforward; no real trade-off worth recording.

**Effort:** M (~1-1.5 days).

### P2.W2.4 — A3 init: `--one-shot --kv` mode

**Phase 1 reference:** `805179c feat(cmd): init wizard`.

**Files:**
- `cmd/init.go` — `--one-shot` flag plus `--kv key=value`
  (repeatable). When set, skip every prompt; build the Config
  from the KV pairs directly. Recognised keys mirror the
  wizard prompts: `name`, `url`, `kind`, `prefix`, `tenant`,
  `auth_mode`, `bearer_token`, `basic_user`, `basic_password`,
  `poll_interval`, `theme`. Unknown keys fail closed.
- `cmd/init_test.go` — table-driven coverage of the kv-only
  flow + missing-required-key errors.

**Effort:** S (~half day).

### P2.W2.5 — silences / groups / receivers list commands

**Phase 1 reference:** `ba7dee3` (the QA-driven amend that added
`alerts list`). Mirror that shape three times:

**Files:**
- `cmd/silences.go` — `silences list` with `--state` (active /
  expired / pending), `--matcher` (Prom matcher), `--fail`.
- `cmd/groups.go` — `groups list`, optional filter flags.
- `cmd/receivers.go` — `receivers list`. Smaller payload; no
  filter flags initially.
- Per-command tests for the helpers (filter / sort / render).
- Register all three in `cmd/cmd.go` under `groupRead`.

**Effort:** M (~1 day; mostly copy-paste from `cmd/alerts.go`).

### P2.W2.6 — D3 probe cache

**Phase 1 reference:** `ce1b375 feat(doctor)`.

**Files:**
- `internal/doctor/cache.go` (new) — small in-memory TTL cache
  keyed by `(tenant, check_name)`. 30s TTL. Wraps each Checker
  via a decorator pattern.
- `cmd/doctor.go` — wrap the resolved checker set in
  `doctor.WithCache(cs, 30*time.Second)` when invoked
  repeatedly (e.g. from a future TUI status-page refresh).

**Effort:** S (~half day). Tested via fake clock.

---

## ADR shortlist

Per the strict bar (hard-to-reverse + surprising + real
trade-off):

- **`docs/adr/0010-user-keybindings-schema.md`** — C3 schema
  shape, reserved-key carve-out, conflict resolution policy.
  User-facing config contract; future schema breaks would
  invalidate every shipped `keys/<profile>.yaml`.

Other phase-2 items don't clear the bar:
- D4/D2 follow-ons mirror phase 1 mechanically — no new
  decisions.
- A1 doctor extensions reuse the existing Checker pattern.
- A3 `--one-shot` is additive on the existing init flow.
- alerts/silences/etc. list parity reuses the established
  shape.
- A9 config-dir merge follows YAML-merge precedent.
- E1/E8/F4/G3/G4/G5/B5 are isolated UX bits.

---

## Verification plan

Per-commit shape mirrors phase 1:

1. **TDD** — tests in the same commit. Coverage: happy path +
   ≥1 edge case.
2. **`prek -a`** — full pre-commit; every hook passes.
3. **`go test ./...`** — green from package root.
4. **Subagent review loop** — `general-purpose` agent with the
   3-priority × 3-category prompt; address need-work items;
   re-review until clean (or only nits remain by choice).
5. **Manual smoke** — for CLI commands, invoke against a
   fixture in `cmd/smoke/` (extend the existing dir). For TUI
   changes, exercise the surface against `examples/demo.yaml`
   and verify in a real terminal before commit.

End-to-end after Wave 1 + Wave 2:
- `a10r alerts list / silences list / groups list / receivers
  list` — all four headless surfaces respond.
- `a10r doctor` reports TLS expiry + capability mismatches +
  clock skew in addition to the phase-1 checks.
- `a10r init --one-shot --kv name=prod --kv url=https://...`
  produces the same YAML as the interactive flow.
- TUI: `w` toggles watch mode on every list page consistently.
- TUI: any backend going unreachable shows an error band on
  every list page consistently.
- TUI: `y` on alert / silence detail toggles raw YAML.
- TUI: `~/.config/a10r/aliases.yaml` user aliases resolve in
  the `:` prompt; conflicts at startup fail loud.
- TUI: scrolling with the mouse wheel works on table pages and
  in the help modal.

---

## Critical files (touched by ≥2 candidates)

- `cmd/cmd.go` — three new list commands register here.
- `internal/tui/page/{silences,groups,receivers}/` — D2 + D4 +
  list-cmd parity all touch these.
- `internal/doctor/checks.go` — three new checkers + cache
  decorator.
- `internal/config/loader.go` — A9 merge + C3 keybindings load
  here.
- `internal/tui/keys/dispatch.go` — C3 schema applies here.

---

## What this plan deliberately does not include

- B7 sidebar (L-effort, scope shift toward different nav).
- G1 plugin system (M-effort, security/scope design needed).
- B6 inline bars (status-page widget; deferred for context
  budget reasons, not user-visible enough for v0.0.1).
- C5/C6 theme codegen + runtime CSI (niche, depends on C1
  which was DROPped).
- F1 silence template engine (sprig dep; teams-flavoured).
- E2 focus tracker (no multi-region page yet).
- E4 saved queries (config schema work; v0.1.x).
- F2 layered filters (F4 supersedes user side; structured path
  stays for headless `--filter` later).
- The launch plan's Phase 1-8 work — that's an orthogonal
  release-engineering thread.

---

## Handoff notes for the resuming agent

1. **Branch off `main`.** The user merges
   `feat/v0.0.1-pre-launch-batch` into `main` once this plan is
   accepted, so phase 1 (commits `3cdfb37 … cce0664` plus the
   merge) is on `main` by the time the resuming agent starts.
   New branch: `feat/v0.0.1-phase-2-batch` from `main`.
2. The two ADRs from phase 1 (`docs/adr/0008`, `docs/adr/0009`)
   are the conventions for new ADRs in phase 2 — terse,
   1-3-sentence body, optional sections only when they earn
   their keep.
3. Auto-mode-friendly: every item ships independently. The
   orchestrator can pick any Wave 1 item without coordination
   with parallel work; Wave 2 items each have a single
   phase-1-commit dependency that's already on `main`.
4. `prek -a` and `go test ./...` are gates on every commit.
   `go install .` to refresh the `$PATH` binary between TUI
   smoke checks (the launch-batch QA round taught us how
   easily a stale `~/.local/bin/a10r` masks fixes).
5. **Read the "Context-window discipline" section above before
   starting.** The orchestrator pattern is not optional; it is
   the reason this plan exists as a separate phase.

---

## Sub-agent prompt template

The orchestrator should adapt this template per item — fill in
`<…>` placeholders, leave the rest verbatim. Keep the prompt
focused: one item, one sub-agent, one commit. Do not bundle
multiple plan items into one delegation.

```
You are implementing a single item from a10r's phase 2 punch list,
plan at /home/debian/workspace/github.com/wilfriedroset/a10r/docs/design/phase-2-plan.md.

Item: <P2.W1.N or P2.W2.N>
Title: <copy the heading>

Scope (verbatim from plan):
<paste the Files / Effort / ADR block for the item>

Project conventions (must follow):
- TDD: tests in the same commit, happy path + ≥1 edge case.
- prek -a clean before commit (golangci-lint, govulncheck, SPDX,
  trailing whitespace, …).
- One commit per logical unit, conventional message body
  explaining "why", not just "what".
- Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  trailer.
- No emojis in code, comments, or commit messages.
- Go 1.25.0 (loop var capture fixed; no `x := x` shadowing).
- Many small files over few large.
- DI / table-driven tests / small focused units.

Phase 1 ADRs at docs/adr/0008-http-debug-redaction.md and
docs/adr/0009-exit-code-table.md set the format precedent for
any new ADR.

Phase 1 reference commit (if applicable): <git sha + one-line
summary; read this commit's diff for the canonical shape>.

Deliver:
1. Implementation + tests in one commit on the active branch.
2. prek -a green, go test ./... green.
3. Trigger the project's clean-commits review loop:
   "spawn a sub agent to review the changes. focus the review
   on 3 priorities: need-work, nice to have, nits and 3
   categories: maintainability/testability/scalability, golang
   idiomatic."
4. Address need-work findings; iterate until the review comes
   back clean (or only nits remain by judgement).
5. Return ONE message summarising: commit SHA, files touched,
   review-loop iterations, any deferred sub-items the review
   surfaced.

Do not implement other plan items, even if they look related.
Do not refactor unrelated code. Stay narrow.
```

The orchestrator runs the review loop's outer cycle (delegate
→ verify gates → next item). It does not write code itself.
