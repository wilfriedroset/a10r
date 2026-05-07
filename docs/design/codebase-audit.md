# Codebase audit (cleanup punch list)

## Context

a10r is at a fork: keep building features from
`docs/design/scouting-tui-influences.md`, or pause to clean up the
codebase first. Three signals tipped this toward cleanup:

- **Recent feature pain.** Adding sort, the cmdbar suggester, and
  table-layout work made the duplication between alert / silence /
  group page handlers concrete (not theoretical).
- **Re-reading cringe.** Six files breach the CLAUDE.md 800-LOC
  ceiling: `silences.go` (1889), `alerts.go` (1758), `groups.go`
  (1187), `app.go` (886), `alert.go` (866), `form.go` (828).
- **Anticipating contributors.** The current shape isn't
  newcomer-friendly; a contributor opening `silences.go` is staring
  at 28 funcs in one file.

This doc is the **punch list** for that cleanup. Each item carries a
file:line ref, a category, a severity, and a proposed remediation.
When every item is fixed or explicitly deferred (with a rationale),
cleanup is done and feature work resumes. Even then, scouting-doc
candidates land one at a time, on explicit per-candidate decision —
the audit completing is not a license to start picking from
`scouting-tui-influences.md`.

## Scope

In:

- **Maintainability + structure** — file size, function size,
  package boundaries, dead code, naming.
- **Code duplication** — byte-identical and structurally-identical
  patterns across pages and across chrome packages.
- **Anti-over-engineering** — interfaces with one impl and no
  imminent variant, defensive checks for impossible internal states,
  abstractions consumed by ≤1 caller, error wrappers with no info
  gain.

Out (called out so they don't sneak back):

- **Performance / resource usage** — workload shape (poll +
  render ≤ a few thousand items) doesn't justify a perf pass yet.
- **Security review** — bearer-token, no PII, fuzzing already landed
  for app shell + silence form (b5e48eb / fc7b580). Lightweight pass
  is a separate, smaller doc when we get there.
- **Contributor-facing docs** — better code IS more
  contributor-friendly; defer dedicated CONTRIBUTING/onboarding
  until the structural cleanup lands.
- **Speculative abstractions** — no scouting feature is picked yet;
  do not pre-shape abstractions for hypothetical future callers.
  Three similar files is better than the wrong base type.

## Methodology

Three Explore subagents ran in parallel, each on a slice:

- **Slice A — TUI pages** (`internal/tui/page/*`,
  `internal/tui/form/silence/`).
- **Slice B — TUI chrome** (`internal/tui/{app,keys,header,footer,
  cmdbar,help,modal,panel,action,theme,tablesort,poll,edit,testutil}/`).
- **Slice C — Backend / config / cmd** (`internal/backend/*`,
  `internal/config/`, `internal/log/`, `cmd/`, `main.go`).

Each agent applied three lenses (maint, dedup, anti-over-eng), used
file-level diffs to verify duplication claims, and reported severity
ranks with proposed remediations. Spot-checked findings before
inclusion: `handleMotion` byte-identical, `padRight` byte-identical,
`reconcileScroll` byte-identical (modulo one comment), `handleSort`
structurally identical (differs only in focus-field name), `app.a.quitting`
written once and never read.

Test coverage map (sets the refactor-confidence baseline):

- 100%: `internal/backend`, `backend/factory`, `tui/action`, `tui/cmdbar`
- 90-99%: `config` (96.2), `tui/app` (94.8), `tui/footer` (94.5),
  `backend/transport` (93.9), `log` (92.2), `tui/keys` (92.1),
  `tui/header` (91.4), `backend/mimir` (91.7), `tui/form/silence` (92.5)
- 80-90%: `backend/vanilla` (86.7), `tui/modal` (81.3)
- 70-80%: `tui/edit` (76.7), `tui/help` (72.3), `backend/multi` (74.2)
- Below 70%: `cmd` (36.5 — thin CLI wiring, acceptable)

Refactor confidence: **high** for everything except possibly
`backend/multi` and `tui/help`.

## Severity legend

- **S** — under a day, isolated change.
- **M** — 1-3 days, may touch multiple files.
- **L** — >3 days, structural change.

## Stance

- **Removal**: aggressive. Default = delete unless proven needed.
  Coverage is high; tests will catch regressions. Long-tail
  defensive code is removed even when "harmless."
- **Abstraction**: conservative. Extract only what is duplicated
  *today* across ≥2 callers and is structurally clean to share. Do
  not invent base types for "framework" purposes.
- **Per-commit discipline**: TDD where behavior changes, `prek -a`
  green before staging, one logical unit per commit (no WIP /
  fix-up history) — matching the `## Principles` section of
  `CLAUDE.md`.

---

## Punch list (execution order)

Ordered to maximise momentum: quick wins first (single-file, no
deps), then dedup extractions (build a tiny shared helpers package
under `internal/tui/page/`), then file splits (which benefit from
having dedup helpers in place), then the L-severity unification.

### Wave 1 — Quick wins (S, no deps)

| # | ID | Title | Slice |
|---|-----|---|---|
| 1 | A2.5 | Extract `loadStyles(t)` to `tui/page/testutil` | pages |
| 2 | B2.1 | Add `theme.FgOnly(c)` and adopt in header + panel | chrome |
| 3 | C1.6 | Extract `vanilla.exec(req, dst)` to dedupe Get/Post/Delete | backend |

Five items dropped at execution-time spot-check — B1.2
(`app.quitting`), A1.4 (`alert.View` receiver), C1.3
(`validateAuthExclusive` slice), C2.1 (factory ↔ vanilla doc was
already in place at `mimir/client.go:74-80` and `factory.go:24-42`),
B1.7 (`hasBinding` already delegates to `lookup`, no duplicate
walk). See "Watched, not actioned" rows below for each rationale.
B2.1 went the other way — the audit named three sites; execution
surfaced ten more under the same shape, all converted in cecb8f4.

Net Wave 1 false-positive rate (5 of 8) is a process lesson: the
audit subagents didn't sweep existing tests / existing comments
/ existing helper-delegation, and one item overstated the
cost-benefit. Subsequent waves should expect a similar correction
loop, and execution-time spot-check should precede every commit.

### Wave 2 — Page dedup extractions (M, all sit under `internal/tui/page/`)

These create the small shared helpers package the file splits in
Wave 3 will lean on. Each extraction is its own commit; downstream
adoption follows in subsequent commits per page.

| # | ID | Title | Output |
|---|-----|---|---|
| 9 | A2.1 | Extract motion handling | `internal/tui/page/cursor/motion.go` |
| 10 | A2.3 | Extract scroll reconciliation | `internal/tui/page/cursor/scroll.go` |
| 11 | A2.4 | Extract `padRight` / `truncate` | `internal/tui/page/format/text.go` |
| 12 | A2.2 | Extract sort key dispatch with `clearFocus` callback | `internal/tui/page/cursor/sort.go` |
| 13 | B2.4 | Factor modal Enter/Esc/nav boilerplate to a base helper | `tui/modal/base.go` |

Adoption commits (one per page, per helper) follow each extraction.

### Wave 3 — File splits (L, page-by-page)

Each page split is one or two commits. Helpers from Wave 2 are
already available, so the splits stay narrow (no
"refactor-while-splitting"). Suggested layout per page:

- `<page>.go` — `Page` struct, `New`, `Init`, `Close`, `Title`,
  `Bindings`, `Crumb`.
- `handlers.go` — `Update`, `handleKey`, `handleMotion`,
  `handleSort`, `handleAction`, `handle{Sideband,Filter}Prompt`,
  `handleWriteResult`.
- `render.go` — `HeaderContent`, `Footer`, `View`, `renderHeader`,
  `renderRows`, `renderCells`, `padColumns`, `columnWidths`.
- `state.go` — `recompute`, `snapshotFocus`, filter / scope helpers,
  scroll helpers, `polled`, `spinnerActive`, etc.

| # | ID | Title |
|---|-----|---|
| 14 | A1.1 | Split `silences.go` (1889 → ~4 files) |
| 15 | A1.2 | Split `alerts.go` (1758 → ~4 files) |
| 16 | A1.3 | Split `groups.go` (1187 → ~3-4 files) |
| 17 | B1.1 | Split `app.go` (886 → lifecycle / input / pagestack) |

`alert.go` (866) and `form.go` (828) are right at the edge; review
during their adoption commits — split only if the seam is natural.

### Wave 4 — Chrome / backend tightening (M, independent)

| # | ID | Title | Slice |
|---|-----|---|---|
| 18 | B1.3 | Collapse `registerGlobalBindings` / `registerTenantBindings` to a single helper | chrome |
| 19 | B1.4 + B2.2 | Source help catalogues from the dispatcher's registered bindings | chrome |
| 20 | B1.6 | Collapse `theme.compile*` into table-driven `compileStyle(role, fgChain, bgChain)` | chrome |
| 21 | C2.5 | Generic K1 resolver helper (`cli > env > file > default`) | config |
| 22 | C1.5 | Extract TUI build orchestration (optional — only if `cmd/tui.go` keeps growing) | cmd |

### Wave 5 — Cross-package retry unification (L)

| # | ID | Title |
|---|-----|---|
| 23 | B2.3 | Extract `internal/retry/exponential.go` (`Backoff{Base, Cap, Jitter}` + `NextDelay(failures)`); adopt in `tui/poll` and `backend/transport` |

Last because it's the highest-blast-radius change and benefits from
landing after the page/chrome cleanups stabilise the call sites.

---

## Inventory by slice

### Slice A — TUI pages

#### A.1 Maintainability

| ID | File:line | Sev | Smell | Remediation |
|---|---|---|---|---|
| A1.1 | `internal/tui/page/silences/silences.go:1-1889` | L | 1889 LOC, 28 funcs spanning model / handlers / render / IO in one file | Split per Wave 3 layout |
| A1.2 | `internal/tui/page/alerts/alerts.go:1-1758` | L | 1758 LOC, 28 funcs | Same split |
| A1.3 | `internal/tui/page/groups/groups.go:1-1187` | L | 1187 LOC; motion / sort inlined into `handleKey` | Split + inline-extract motion / sort to match alerts / silences |
| A1.4 | `internal/tui/page/alert/alert.go:360` | — | False positive — `View` does use `p` (`p.bodyHeight`, `p.bodyLines`, `p.reconcileScroll` at lines 364-366) | **Closed** at execution-time spot-check |

#### A.2 Duplication

| ID | Files | Sev | Pattern | Remediation |
|---|---|---|---|---|
| A2.1 | `alerts.go:704-735` + `silences.go:711-742` (also inlined in `groups.go:612-647`) | M | `handleMotion` byte-identical (j/k/G/Ctrl+D/U/F/B + snapshotFocus) | Free function in `tui/page/cursor/motion.go`; pages call it from their own handler |
| A2.2 | `alerts.go:746-760` + `silences.go:752-765` + `groups.go:668-681` | M | `handleSort` structurally identical; differs only in focus-field name (`focusFingerprint` vs `focusID` vs `focusKey`) and comment wording | Helper accepting `clearFocus func()` callback in `tui/page/cursor/sort.go` |
| A2.3 | `alerts.go:1523-1538` + `silences.go:1656-1670` + `receivers.go:421-436` | M | `reconcileScroll` byte-identical (one comment differs in alerts) | Pure function in `tui/page/cursor/scroll.go` |
| A2.4 | `alerts.go:1601-1625` + `silences.go:1760-1785` | M | `padRight` + `truncate` byte-identical | `tui/page/format/text.go` |
| A2.5 | `alerts_test.go:36-41` + `silences_test.go:35-40` + `groups_test.go:24-29` | S | `loadStyles(t)` byte-identical | `tui/page/testutil.LoadStyles` |
| A2.6 | `alerts_test.go:57-63` + `silences_test.go:42-48` | S | `newPage(t)` factory similar; types differ | Skip — payoff too low |

#### A.3 Anti-over-engineering

All four candidates flagged in the slice (`silences.Client`,
`alert.Clipboard / Browser`, `tenantconfig.StatusFetcher`, the
`Now == nil` defaults) reviewed and **kept**. Each is a small
explicit I/O boundary or an idiomatic Go zero-value default. Listed
under "Watched, not actioned" below for traceability.

### Slice B — TUI chrome

#### B.1 Maintainability

| ID | File:line | Sev | Smell | Remediation |
|---|---|---|---|---|
| B1.1 | `internal/tui/app/app.go:68-122` | M | 35+ methods in one struct: lifecycle, input, page-stack, modal, theme reload interleaved | Split per Wave 3 layout (`lifecycle.go` / `input.go` / `pagestack.go`) |
| B1.2 | `internal/tui/app/app.go:93,411` | — | Field is read by three test assertions (`app_test.go:297,348,357`) as the routing-observability seam for QuitMsg / capturing-page tests — audit subagent missed test files | **Kept** — see "Watched, not actioned" |
| B1.3 | `internal/tui/app/app.go:201-246` | S | `registerGlobalBindings` / `registerTenantBindings` duplicate the `dispatcher.Set(...)` boilerplate | Single helper `registerBinding(layer, key, desc, handler)` |
| B1.4 | `internal/tui/app/app.go:277-312` | S | Three help-catalogue funcs hand-list keybindings in parallel with `dispatcher.Set` calls | Source from `Dispatcher.Bindings(layer)` accessor |
| B1.5 | `internal/tui/panel/panel.go:42-64` | S | `styleTitle` regex parsing has no tests | Add unit tests for `titleStructRE` edge cases (or simplify if regex is overkill) |
| B1.6 | `internal/tui/theme/styles.go:240-620` | M | `compile()` plus 10 thick `compile*()` wrappers around cascading `firstSet(fg, bg)` chains | Collapse to table-driven `compileStyle(role, fgChain, bgChain)` |
| B1.7 | `internal/tui/keys/dispatch.go:200-213` | — | False positive: `hasBinding` already delegates to `lookup` (no duplicate walk), and the proposed merge adds a `Layer` return value no caller needs (speculative API surface) | **Closed** at execution-time spot-check |
| B1.8 | `internal/tui/poll/poll.go:133-195` | M | 401 LOC inline state machine (idle → pending → retry); no helper extraction | Extract `backoffDelay`, `jitterize`, `handleResult(ok, err) tea.Cmd` |
| B1.9 | `internal/tui/footer/prompt.go:160-280` | — | 12 methods on a stateful text buffer; `Update` handles all keys inline | **Keep** — coverage is strong (94.5%); not a smell, just dense |

#### B.2 Duplication

| ID | Files | Sev | Pattern | Remediation |
|---|---|---|---|---|
| B2.1 | `tui/header/header.go:146` + `tui/panel/panel.go:317` | S | Both define a foreground-only `lipgloss.Style` factory off a palette role (`Header.Default` and `Hint.Default`); two callers, one-liner pattern | Add `theme.FgOnly(c color.Color) lipgloss.Style`, adopt in both. (Audit also named `tui/footer/flash.go:124` but that's a palette-dispatch by level — different pattern, not in scope.) |
| B2.2 | `tui/app/app.go:256-289` ↔ `tui/help/help.go:127-145` | M | Globals / table catalogues curated in `app.go`; `help.columns` rebuilds parallel static lists | Pass `[]action.Action` from app to help at open-time; help becomes pure view |
| B2.3 | `tui/poll/poll.go:133-195` ↔ `backend/transport/transport.go:*` | L | Both implement exponential backoff + jitter independently | Extract `internal/retry/exponential.go`; adopt in both |
| B2.4 | `tui/modal/picker.go` + `tui/modal/confirm.go` | S | Identical Enter/Esc/nav routing scaffolding | `tui/modal/base.go` helper; both modals embed |

#### B.3 Anti-over-engineering

| ID | File:line | Disposition | Note |
|---|---|---|---|
| B3.1 | `tui/keys/dispatch.go:66-178` | **Watch** | Multi-key chord state machine ships for `gg` only today. Keep until v0.2 review confirms no other chord arrives. |
| B3.2 | `tui/action/action.go:94-154` | **Watch** | `Bulk` flag defined but unread. Keep — wired with the dispatcher's bulk-action filter; first per-page consumer arrives with a future scouting feature. |
| B3.3 | `tui/app/app.go:102-121` | Keep | `pollCache` is bounded and load-bearing for responsive page opens. |
| B3.4 | `tui/modal/modal.go:36-65` | Keep | Three impls (Picker, Confirm, Help) — the interface is justified. |
| B3.5 | `tui/theme/{schema,styles}.go` | Keep | k9s schema mirroring is intentional for skin-drop-in parity. |
| B3.6 | `tui/panel/panel.go:114-222` | Keep | Five render helpers compose cleanly; each is well-tested. |

### Slice C — Backend / config / cmd

#### C.1 Maintainability

| ID | File:line | Sev | Smell | Remediation |
|---|---|---|---|---|
| C1.1 | `backend/transport/transport.go:250-268` | S | `buildProxyFunc` returns closure with internal `compileNoProxy` call | Low payoff — keep |
| C1.2 | `backend/transport/transport.go:283-318` | S | `compileNoProxy` is dense with nested closures, no dedicated tests | Add unit tests for port stripping / suffix vs exact match; OR introduce a small `proxyMatcher` value type |
| C1.3 | `internal/config/types.go:158-173` | — | Original allocates 0-1 times in the common path; runs once per backend at startup; alternatives sketched (counter + lazy slice; three booleans) were equal or worse on clarity | **Kept** — see "Watched, not actioned" |
| C1.4 | `internal/config/types.go:122-144` | — | `Backend.Validate` calls six helpers in sequence | **Keep** — composition is clear, error short-circuit is correct |
| C1.5 | `cmd/tui.go:48-615` | M | 615 LOC, 20 helper funcs sized 14-110 LOC | Optional: extract a `TUIBuilder`. Defer until the file grows past 700 LOC or a future `init` / `doctor` command lands |
| C1.6 | `backend/vanilla/client.go:128-192` | S | `doGet` / `doPost` / `doDelete` repeat context + error-classify boilerplate | Extract `exec(req, dst any)` |
| C1.7 | `internal/config/types.go` | — | 375 LOC, dense | **Keep** for now — split when schema grows past v0.1 surface |

#### C.2 Duplication

| ID | Files | Sev | Pattern | Remediation |
|---|---|---|---|---|
| C2.1 | `backend/factory/factory.go` | — | Doc already exists: `mimir/client.go:74-80` explains why `mimir.New` returns `*vanilla.Client`, and `factory.go:24-42` documents the single-code-path decision | **Closed** at execution-time spot-check — audit subagent didn't read the existing comments |
| C2.5 | `internal/config/resolve.go:121-193` | S | Five resolvers each implement `cli > env > file > default` precedence | Generic helper `resolve[T](cli, file T, env func() T, def T) T` |

C2.2-C2.4 reviewed and dismissed (RT constructors / smoke binary /
filter encoders are minimal repetition with no clean shared shape).

#### C.3 Anti-over-engineering

All eight candidates reviewed and **kept**:

- `factory/factory.go` — single-path factory is justified; doc-only.
- `multi/multi.go` — fan-out is lean; deliberately not a `Backend.Client`.
- `interpolate.go` — single caller is OK for a foundation utility.
- `Defaults.*OrDefault` accessors — intentional zero-value pattern.
- `Capabilities` reserved fields — schema slot, not speculation.
- `MimirConfig` / `TenantConfig` / `Ring` stub types — same reasoning.
- `cmd.GlobalFlags` type alias — prevents accidental field drift.
- `backend/errors.go` vs `vanilla/errors.go` split — correct layering.

Listed under "Watched, not actioned" for traceability.

---

## Watched, not actioned

These were flagged by the audit but are **not** action items. They
remain in the codebase by design. Listed here so future audits don't
re-discover them.

| ID | What | Why kept |
|---|---|---|
| A1.4 | `alert.View` receiver | Receiver is used (`p.bodyHeight`, `p.bodyLines`, `p.reconcileScroll`); audit subagent missed the body — no smell |
| B1.2 | `app.quitting` field | Test observability seam for QuitMsg routing assertions (`app_test.go:297,348,357`). Removing it would require either restructuring three tests or adding an alternate test seam — net negative cleanup |
| C1.3 | `validateAuthExclusive` slice | Runs once per backend at startup; common path (0-1 auth methods) allocates 0-1 times — immaterial. Sketched alternatives (counter + lazy slice; three booleans) were equal or worse on clarity. Audit overstated the smell |
| C2.1 | factory ↔ vanilla doc | Already documented: `mimir/client.go:74-80` explains why `mimir.New` returns `*vanilla.Client`, `factory.go:24-42` documents the single-code-path decision. Audit subagent didn't read existing comments |
| B1.7 | dispatcher `lookup`/`hasBinding` | `hasBinding` already delegates to `lookup` (no duplicate walk); merging by adding a `Layer` return value would expand API surface no current caller needs; helper's single call site is readability-positive vs. inlining |
| A3.1 | `silences.Client` interface | Explicit per-page I/O boundary; small, cohesive |
| A3.2 | `alert.Clipboard` / `Browser` | Nil-able external-side-effect interfaces; design-safe |
| A3.3 | `tenantconfig.StatusFetcher` | Same pattern as A3.2 |
| A3.4 | `Now == nil` defaults | Idiomatic Go optional-injection |
| B1.9 | `footer/prompt.go` density | Test coverage strong; readability fine |
| B3.1 | Dispatcher chord support | Required for future bindings; revisit at v0.2 |
| B3.2 | `action.Bulk` flag | Wired but unread; first consumer arrives with a scouting feature |
| B3.3 | `app.pollCache` | Bounded; necessary for snappy page opens |
| B3.4 | `modal.Modal` interface | Three impls justify the contract |
| B3.5 | k9s skin schema thickness | Drop-in parity is the point |
| B3.6 | `panel.go` render fan-out | Well-factored, well-tested |
| C1.4 | `Backend.Validate` dispatch | Sequential composition is intentional |
| C1.7 | `config/types.go` density | Acceptable at v0.1 size |
| C3.1 | Single-path factory | Doc comment will follow |
| C3.2 | `multi.Client` not a `Backend` | Deliberate — forces single-tenant for writes |
| C3.3 | `interpolate` single caller | Foundation utility |
| C3.4 | `*OrDefault` accessors | Optional-override convention |
| C3.5 | `Capabilities` reserved fields | Post-v0.1 schema slot |
| C3.6 | Stub backend types | Same reasoning as C3.5 |
| C3.7 | `cmd.GlobalFlags` alias | Field-drift prevention |
| C3.8 | Errors split | Interface vs impl layering |

---

## Exit criterion

Cleanup is **done** when:

1. Every Wave 1-5 punch-list item is either fixed or explicitly
   deferred (with a one-line rationale appended to its row).
2. `prek -a` and `make test-race` are green on the resulting branch.
3. No file in the repo exceeds 800 LOC. (`alert.go` and `form.go`
   may stay if their seams aren't natural — explicit deferral
   counts.)
4. No two functions across packages are byte-identical.
5. The "Watched, not actioned" section is reviewed once at the end
   to confirm nothing graduated to actionable during the work.

When all five hold, switch to features. Which feature lands next
is a separate conversation: scouting candidates ship one at a time,
on explicit per-candidate decision.

## Tracking

This doc is the source of truth. Each wave lands as multiple atomic
commits, matching the project's clean-commit principle (one
logical unit per commit, no WIP):

- One commit per extraction in Wave 2.
- One commit per page split in Wave 3.
- One commit per item in Waves 1, 4, 5.

Mark items done by appending ` ✓ <commit-sha>` to the item's row in
the punch-list tables above. When the table reads all-checked,
cleanup ships.
