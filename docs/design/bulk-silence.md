# Bulk silence + bulk expire: implementation plan

Status: ready to implement. Self-contained; do not replay the originating conversation to act on this.

## Goal

Collapse the bulk variants of silence-create and silence-expire into the same single-row binding the user already has muscle memory for, k9s-style. After this series:

- **Alerts list `s`**: no marks → cursor-row silence (current behaviour, unchanged). Any marks (N≥1) → bulk codepath (form prefilled with metadata only, fanout one `CreateSilence` per marked alert with that alert's labels as matchers).
- **Silences list `x`**: no marks → cursor-row expire (current behaviour, refactored). Any marks (N≥1) → bulk codepath (fanout one `ExpireSilence` per marked silence). Same single binding, same handler.
- **`Ctrl+S`** (alerts) and **`Ctrl+X`** (silences) are removed everywhere — keybindings catalog, action registry, header chips, troubleshooting. The single-binding rule is the whole point.
- **`Ctrl+\`** clears marks on whichever page owns them. New global binding; both pages listen.

The bulk path shares one fan-out machine: per-tenant client dispatch, configurable concurrency (`defaults.bulk_concurrency`, default 4), sequential within tenant when concurrency=1, parallel across tenants. Failures keep their marks; successes drop theirs. One summary flash. No progress bar in v1 — k9s parity.

## Pre-implementation verifications

Run before starting; skip the series if any drifted.

1. **Single-`s` on alerts is wired.** `internal/tui/page/alerts/alerts.go:openSilenceFormForCursor` (~line 702) pushes `silenceform.New` prefilled with the cursor alert's labels. The `marks` map exists on the page (~line 164) plus `Space` toggle and a `MARK` indicator path; `Ctrl+S` is **not** wired (placeholder docstring at line 160 still references "#30").
2. **Silences page write surface is fully wired** per `silence-write-surface.md`. Single-`x` confirms via `openExpireConfirm` (~line 797), bulk `Ctrl+X` confirms via `openBulkExpireConfirm` (~line 824), both run through `handleExpireConfirm` (~line 862). Today the loop is sequential, synchronous, and **clears every queued mark regardless of success/failure** (~line 885) — this commit series flips that to "unmark on success, keep failed marked."
3. **Form `Bulk` mode does not exist.** `internal/tui/form/silence/form.go:Options` has no Bulk / BulkBanner fields. Submit always calls `Client.CreateSilence` or `Client.UpdateSilence`. Bulk mode is added in commit 2.
4. **No bulk-concurrency knob exists.** `grep -n "Concurrency\|bulk" internal/config/*.go` returns nothing. Added to `config.Defaults` in commit 1.
5. **`Ctrl+\` is unbound.** Confirm with `grep -rn 'ctrl+\\\\\\\\' internal/`. Added in commit 5.
6. **Header chip / action registry / keybindings docs still advertise `Ctrl+S` and `Ctrl+X`**:
   - `internal/tui/header/header_test.go:220,251,258` — `Ctrl+S` chip
   - `internal/tui/action/action_test.go:91` — `Ctrl+S` registration
   - `internal/tui/page/silences/silences.go:462` — `Ctrl+X` Bindings entry
   - `docs/design/keybindings.md:53,55,114,143,243` — design catalog
   - `docs/end-users/keybindings.md:51,76,116`, `docs/end-users/troubleshooting.md:38-40` — user-facing catalog + workaround note
   All updated in commit 6.

## Settled design decisions

- **Same key, different N** (k9s parity, verified `derailed/k9s/internal/ui/select_table.go:46-60`). The dispatcher routes `s` / `x` to one handler per page; the handler branches on `len(p.marks)`. This is intentionally the same UX shape as k9s `Ctrl+D` for pods: marks if any, cursor otherwise.
- **Bulk-create form is the single-silence form in "bulk mode"**, not a new component. `silenceform.Options` gets a `Bulk bool` and `BulkBanner string`. In bulk mode the form: hides the matcher buffer, renders the banner where it lived, skips the "at least one matcher is required" validation, and on submit emits `BulkSubmittedMsg{Comment, StartsAt, EndsAt, Creator}` (auto-pop). The matcher-substitution-per-alert is the page's job — the form never knows the targets exist.
- **Confirm modal — bulk-create**: shown for `N≥2` (single-mark `s` skips the confirm because the form itself is the gate). Default-Yes; silence-create is reversible. Message format:
  - Single tenant: `silence 5 alerts? (tenant prod)`
  - Multi tenant: `silence 15 alerts? (tenant prod=12, staging=3)`
- **Confirm modal — bulk-expire**: shown for **all** N≥1 (no form, the confirm is the only gate). Default-No; alerts re-fire the moment a silence is expired and on-call may page. Message format:
  - N=1: `expire silence <id>?` (current single-row wording, unchanged)
  - N≥1, single tenant: `expire 5 silences? (tenant prod)`
  - N≥1, multi tenant: `expire 15 silences? (tenant prod=12, staging=3)`
- **Already-expired marks**: send DELETEs anyway. AM `DELETE /silences/{id}` is idempotent — second DELETE on an expired silence is a no-op or 404, which we count as success. Simpler code, matches k9s's "trust the API" stance.
- **Failure handling** (both flows): unmark on success, keep mark on failure so retry is one keystroke. Flash format:
  - All success: `silenced 15 alerts` / `expired 15 silences`
  - Partial: `silenced 12 of 15 — 3 failed (see :silences)` / `expired 12 of 15 — 3 failed`
  - Per-failure detail goes through structured logging at error level (`backend`, `tenant`, `silence_id` / `alert_fingerprint`, `err`).
- **Concurrency**: `defaults.bulk_concurrency`, integer ≥1, default **4**. Bounded worker pool *per tenant*; tenants run in parallel. `bulk_concurrency: 1` collapses to fully sequential. Validated >0 at config load.
- **Marks survive scope/filter/tenant switches** on both pages. Verified k9s does the same (`internal/view/browser.go:583-592`). `Ctrl+\` is the explicit clear; nothing else clears marks except a successful submit-or-expire that consumes them.
- **No progress bar in v1.** Single completion flash, k9s-style. Revisited in v0.2 if real usage shows the dead air on >100 marks is a problem.
- **Cancellation**: Esc / page-pop during fanout. The fan-out goroutines share a `context.Context` derived from a cancel func stored on the page; cancel cancels the not-yet-started work via the worker-pool channel. In-flight requests run to completion (AM create is non-idempotent, expire is idempotent — both safe to let finish).
- **`__name__` label** stays out of bulk-create matchers, same as single-`s` (drop via the existing `silenceform.MatchersFromLabels` helper).
- **Comment / duration / creator are uniform** across all silences in a single bulk-create. The user fills the form once; every silence gets the same metadata, only the matcher-set differs per alert.

## Commit sequence

Six commits. Each is independently reviewable, has its own tests, and is `prek -a` clean. Subagent review per the project's clean-commits memory after each non-doc commit.

### Commit 1 — `feat(internal/config): add defaults.bulk_concurrency knob`

Pure config addition. No consumer yet — guarantees the knob lands cleanly without race-with-callers.

Files:

- `internal/config/types.go`:
  - `Defaults` (line ~312) gets `BulkConcurrency int yaml:"bulk_concurrency,omitempty"`.
  - Doc on the field: positive integer, default 4 when omitted, applied uniformly to bulk silence-create and bulk silence-expire fan-outs.
  - Add a `Defaults.Validate()` method (or extend the existing `Config.Validate` walker) rejecting `bulk_concurrency < 0`. Zero is allowed → reads as "use default 4".
- `internal/config/resolve.go` (or wherever defaults are materialised — locate via `grep -n PollInterval internal/config/resolve.go`): expose `func (d Defaults) BulkConcurrencyOrDefault() int` returning 4 when zero, else the value.
- `examples/demo.yaml` (or whatever the bundled example is — `grep -rn 'poll_interval' examples/`): add a commented-out `bulk_concurrency: 4` line under `defaults:` so users see the knob.

Tests (`internal/config/types_test.go` + `resolve_test.go`):

- `TestDefaults_BulkConcurrencyDefaultsTo4WhenOmitted`
- `TestDefaults_BulkConcurrencyRejectsNegative`
- `TestDefaults_BulkConcurrencyZeroResolvesTo4`
- `TestDefaults_BulkConcurrencyExplicitValuePreserved`

Out of scope: any consumer wiring. Commits 3 & 4 read the value via cmd/tui.go.

### Commit 2 — `feat(internal/tui/form/silence): bulk-mode form with banner and BulkSubmittedMsg`

The form gains a second submit shape so the alerts page can reuse it for bulk fanout.

Files:

- `internal/tui/form/silence/form.go`:
  - `Options` gets `Bulk bool` and `BulkBanner string`. Doc explains: bulk mode hides matchers, skips the matcher validation, renders the banner where the matchers buffer would be, and on submit emits `BulkSubmittedMsg` instead of calling `Client.CreateSilence`. `Client` is permitted to be nil in bulk mode (the form never calls it).
  - `BulkSubmittedMsg{Comment string, StartsAt, EndsAt time.Time, Creator string}` — implements `app.AutoPopMsg`. No matchers field; the page substitutes per-target matchers at fanout time.
  - `Form` struct gains a `bulk bool` and `bulkBanner string`.
  - `New(opts Options)`: when `opts.Bulk` is true, set `f.bulk = true`, `f.bulkBanner = opts.BulkBanner`, skip the matchers-prefill branch, advance `focus` past `fieldMatchers` (start on `fieldStarts`).
  - `parseSpec` (~line 417): when `f.bulk`, skip the `len(parsed) == 0` check; the spec returned has empty matchers (page fills them per target).
  - `submit` (~line 224): when `f.bulk`, emit `BulkSubmittedMsg` from the validated metadata; otherwise the existing Create / Update branch.
  - `View` (locate the matchers-rendering block): in bulk mode, render the banner string where the textarea would render, styled as the existing read-only label rows.
  - `Title()`: when `f.bulk`, return `"bulk silence"` (the banner carries the count breakdown — keeps the title slot clean).
  - `Tab` / `Shift+Tab` cursor walk: in bulk mode, `fieldMatchers` is skipped from the cycle.

Tests (`internal/tui/form/silence/form_test.go`):

- `TestForm_BulkModeSkipsMatcherValidation`: bulk + empty matchers + valid metadata → submit succeeds.
- `TestForm_BulkModeEmitsBulkSubmittedMsg`: assert the emitted message type and field values.
- `TestForm_BulkModeRendersBanner`: View output contains the banner string and *not* the matchers placeholder.
- `TestForm_BulkModeNeverCallsClient`: fake Client whose Create / Update both panic; submit must not panic.
- `TestForm_BulkModeTabSkipsMatcherField`: starts focus on `fieldStarts`; Tab cycles starts→ends→creator→comment→starts (no matchers).
- `TestForm_BulkModeTitle`: assert title shape.

Out of scope: callers. Existing single-form callers untouched (Bulk defaults to false).

### Commit 3 — `refactor(internal/tui/page/silences): unify x / Ctrl+X, fan out with concurrency, retain failed marks`

Files:

- `internal/tui/page/silences/silences.go`:
  - `Options` gets `BulkConcurrency int` (zero → 4 via `BulkConcurrencyOrDefault` at the wiring boundary, page receives the resolved value).
  - `handleAction` (~line 665): drop the `case "ctrl+x"` branch. Rename `openExpireConfirm` to keep the semantics ("cursor row when no marks") and change `case "x"`:
    ```go
    case "x":
        cmd := p.openExpireConfirmUnified()
        return p, cmd
    ```
    `openExpireConfirmUnified` branches on `len(p.marks)`: 0 → existing cursor-row path (unchanged wording: `expire silence <id>?` default-No); ≥1 → existing bulk path (new wording with tenant breakdown, default-No).
  - Confirm message: introduce a small helper `formatTenantBreakdown(ids []pendingExpireID) string` returning `"prod"` for single-tenant, `"prod=12, staging=3"` for multi-tenant. Used by the bulk question.
  - Drop the `Ctrl+X` Bindings entry (~line 462).
  - `handleExpireConfirm` (~line 862): replace the sequential synchronous loop with a bounded-pool fan-out:
    1. Group `pending.ids` by tenant.
    2. Per tenant, spawn a goroutine running a worker pool of `min(p.bulkConcurrency, len(group))` workers consuming from a channel of IDs.
    3. Collect results onto a results channel (`{id string; err error}`) until every group is done.
    4. Walk results: success → `delete(p.marks, id)`; failure → keep marked, log structured, increment failed counter.
    5. Flash summary via `flashExpireResult`.
    The Cmd needs to be returned synchronously, but the work is async — wrap in a `tea.Cmd` that blocks on completion and emits a `BulkExpireDoneMsg{success, failed int}` so the Update handler can flash from the message side. Mirror this pattern in commit 4 for create.
  - Add `cancelBulk context.CancelFunc` to the page; populated when fanout starts, called on `Close()` to short-circuit pending workers.
  - Update `silences.go:8` docstring to reflect the unified binding.

- `cmd/tui.go`:
  - Read `cfg.Defaults.BulkConcurrencyOrDefault()` and pass `BulkConcurrency: bulkN` into the silences factory.

Tests (`internal/tui/page/silences/silences_test.go`):

- `TestPage_XKeyNoMarksUsesCursor`: cursor row, no marks, `x` → confirm with single-row wording.
- `TestPage_XKeyWithMarksGoesBulk`: 3 marks, `x` → confirm with bulk wording + tenant breakdown.
- `TestPage_BulkExpireUnmarksOnlySuccessfulIDs`: fake Client returns error for one of three IDs → after the result handler, that one ID is still in `p.marks`, the other two are gone.
- `TestPage_BulkExpireFlashesSummary`: 12-of-15 success → flash text contains "expired 12 of 15".
- `TestPage_BulkExpireRespectsConcurrency`: synchronous fake Client recording call timestamps; concurrency=2 → at most 2 in flight per tenant at any moment.
- `TestPage_BulkExpireCancelsOnPageClose`: start fanout, call `Close()` mid-flight, assert workers exit before processing remaining IDs.
- `TestPage_CtrlXBindingRemoved`: assert `Bindings()` no longer contains a `Ctrl+X` entry.
- Update existing `TestPage_BulkExpireConfirmsAndIteratesMarks` and friends to drive `x` instead of `ctrl+x`.

### Commit 4 — `feat(internal/tui/page/alerts): bulk silence on `s` with marks`

Files:

- `internal/tui/page/alerts/alerts.go`:
  - `Options` gets `BulkConcurrency int`.
  - `openSilenceFormForCursor` keeps its name and behaviour (no-marks path).
  - New `openBulkSilenceConfirm()`: groups marked alerts by tenant (using stored `byTenant` rather than walking the filtered `view`), formats the confirm question with tenant breakdown, default-Yes; stashes a `pendingBulkSilence` field with the resolved targets `[]bulkSilenceTarget{Tenant, Fingerprint, Matchers}`.
  - `handleAction` (~line 658) for `case "s"`: branch on `len(p.marks)`. 0 → `openSilenceFormForCursor` (unchanged). ≥1 → `openBulkSilenceConfirm` for `N≥2`, or fall straight into the bulk form for `N=1` (no confirm — matches the create-side rule "form is the gate when N=1, dedicated confirm only at N≥2"; the form's banner still shows the breakdown).
  - On `ConfirmResultMsg{Yes: true}` for the pending bulk: open the form in bulk mode with banner `applies to <N> alerts across <M> tenants — each silenced with its own labels`.
  - On `silenceform.BulkSubmittedMsg`: kick off fanout. Same per-tenant worker-pool shape as commit 3. Each per-target call: `clients[target.Tenant].CreateSilence(ctx, spec)` where `spec` has the form's metadata + `Matchers: target.Matchers`. Results: success → `delete(p.marks, target.Fingerprint)`; failure → keep marked, log structured. Summary flash via the same shape as bulk-expire.
  - `Close()` cancels the in-flight fanout context.
  - **No** `Ctrl+S` binding — the alerts page never had one wired (it was registered in `action_test.go` only). Commit 6 cleans up that registration.

- `cmd/tui.go`:
  - Pass `BulkConcurrency` into the alerts factory at line 88 + line 204 (mirror silences plumbing).

Tests (`internal/tui/page/alerts/alerts_test.go`):

- `TestPage_SKeyNoMarksUsesCursor`: assert existing single-form push behaviour is preserved.
- `TestPage_SKeyOneMarkPushesBulkFormDirectly`: 1 mark → no confirm modal, form opens in bulk mode with banner mentioning 1 alert + that alert's tenant.
- `TestPage_SKeyTwoMarksOpensConfirmFirst`: 2 marks → confirm modal with tenant breakdown.
- `TestPage_BulkSilencePerAlertMatchers`: 3 alerts with distinct labels → 3 `CreateSilence` calls, each with that alert's labels (minus `__name__`).
- `TestPage_BulkSilencePerTenantDispatch`: 2 alerts on tenant A, 1 on tenant B → A's client sees 2 calls, B's sees 1, no cross-tenant leakage.
- `TestPage_BulkSilenceUnmarksOnlySuccessfulFingerprints`: failed fingerprints stay marked.
- `TestPage_BulkSilenceFlashesSummary`: success/partial/failure flash wording.
- `TestPage_BulkSilenceRespectsConcurrency`: same shape as the silences test.
- `TestPage_BulkSilenceCancelsOnPageClose`.

### Commit 5 — `feat(internal/tui/app): Ctrl+\ clears marks on the focused page`

Cross-page binding. The dispatcher recognises `Ctrl+\` and emits a new `app.ClearMarksMsg`; alerts and silences pages handle it by emptying their `marks` map. No-op on pages without marks.

Files:

- `internal/tui/app/app.go` (or wherever the message types live): define `ClearMarksMsg struct{}`.
- `internal/tui/keys/dispatch.go`: route `Ctrl+\` to emit `ClearMarksMsg`.
- `internal/tui/page/alerts/alerts.go`: handle `app.ClearMarksMsg` → `p.marks = map[string]struct{}{}` + flash `marks cleared` (Info level) when the pre-clear count was >0; silent no-op otherwise.
- `internal/tui/page/silences/silences.go`: same shape, same flash wording.

Tests:

- `internal/tui/page/alerts/alerts_test.go`: `TestPage_ClearMarksMsgEmptiesMarks`, `TestPage_ClearMarksMsgWithNoMarksIsSilent`.
- `internal/tui/page/silences/silences_test.go`: same two.
- `internal/tui/keys/dispatch_test.go`: `TestDispatch_CtrlBackslashEmitsClearMarksMsg`.

### Commit 6 — `docs: drop Ctrl+S/Ctrl+X from keybindings, add Ctrl+\, changelog`

Single docs commit. No code. `prek -a` only — skip the subagent review (project policy).

Files:

- `docs/design/keybindings.md`:
  - Remove the `Ctrl+S caveat` section (lines ~53-55) — no longer relevant once the binding is gone.
  - Drop the `Ctrl+S` and `Ctrl+X` rows from the catalog tables (~lines 114, 143).
  - Drop both from the global-binding-list line (~line 243).
  - Add a `Ctrl+\` row in the global-bindings table: `Clear marks on the focused page`.
  - Note in the alerts-list section: `s` with marks → bulk silence (form once, fanout per alert). Note in the silences-list section: `x` with marks → bulk expire.
- `docs/end-users/keybindings.md`:
  - Drop `Ctrl+S` row (line 51), `Ctrl+X` row (line 76). Update the read-only bullet (line 116) to reference `s` and `x` only.
  - Add `Ctrl+\` row in the global table.
- `docs/end-users/troubleshooting.md`:
  - Remove the `Ctrl+S does nothing on the alerts page` entry (lines 38-40 + heading).
- `internal/tui/action/action_test.go:91`: drop the `Ctrl+S` registration. (Code change but trivially co-located with docs cleanup; if the reviewer prefers strict docs-only, split into 6a/6b.)
- `internal/tui/header/header_test.go:220,251,258`: drop the `Ctrl+S` chip expectations. (Same caveat.)
- `CHANGELOG.md`, under `## [v0.1.0] — TBD`:
  - `s` on the alerts list now silences every marked alert when marks exist (or the cursor alert otherwise). Multi-tenant marks fan out per-tenant; failures keep their marks for retry.
  - `x` on the silences list now expires every marked silence when marks exist. Multi-tenant fanout, idempotent on the AM side.
  - `Ctrl+\` clears marks on the focused page.
  - `defaults.bulk_concurrency` (default 4) tunes the per-tenant worker pool for both bulk silence and bulk expire.

Note: if the action-registry / header-test cleanups feel out of place in a docs commit, split this into 6a (`refactor: drop Ctrl+S/Ctrl+X registrations`) and 6b (`docs: ...`). Reviewer's call.

## Out of scope (deferred, named so the receiving agent doesn't scope-creep)

- **Progress bar / counter during fan-out.** k9s parity for v1; reconsider if real usage shows the dead air on >100 marks is a problem.
- **Per-alert silence customisation in bulk-create.** Comment / duration / creator are uniform across all silences in a single bulk; users who need per-alert variation use single-`s` per row or `Ctrl+E` per silence afterwards.
- **"Silence common labels intersection" mode** on the alerts page (groups page already does this for `s`). If a user wants one silence covering all marked alerts, they can use the groups page or write a single matcher manually.
- **Auto-retry on transient errors.** Writer contract (`internal/backend/client.go:32-36`) explicitly forbids it for create. Retry is the user pressing `s` again on the still-marked rows.
- **Bulk-edit silences.** No path for `e` over marks. Out of scope; `Ctrl+E` per silence is the escape hatch.
- **Cancellation of in-flight requests.** Esc cancels not-yet-started workers via context; in-flight HTTP runs to completion. Hard-cancelling AM Create mid-request risks orphaned silences and isn't worth the complexity in v1.

## Acceptance checks before merging the series

Run after each commit:

- `prek -a` clean.
- `go test -race -timeout 60s ./...` green.
- `golangci-lint run --timeout 60s ./...` clean.

Run after the whole series, against your two-tenant prod config:

- `:alerts`, `Space Space Space`, `s` → confirm modal shows `silence 3 alerts? (tenant prod=2, staging=1)`, default-Yes → form in bulk mode → submit → flash `silenced 3 alerts` → next poll surfaces three new silences across both tenants.
- Same flow but with one tenant's backend down → flash `silenced 2 of 3 — 1 failed`, the failed alert's mark survives.
- `:silences`, `Space Space`, `x` → confirm `expire 2 silences? (tenant prod)`, default-No → Yes → flash success → marks cleared → next poll shows the silences expired.
- `:silences`, single-row `x` (no marks) → existing single-row confirm, unchanged wording.
- `Ctrl+\` after marking five rows → flash `marks cleared`, mark indicator gone.
- `defaults.bulk_concurrency: 1` in config → fan-out is observably sequential (verify via timing or backend-side log).
- `defaults.bulk_concurrency: 8` with 30 marks → fan-out is observably parallel; total wall-clock < (per-request latency × marks).
- `Esc` mid-fanout → not-yet-started work cancels (count of remaining = 0); flash reflects only the work that ran.

## Critical files for reference

- `internal/tui/page/alerts/alerts.go` — single-`s` flow (commit ref `openSilenceFormForCursor` at ~line 702); `marks` map and `Space` toggle pattern; commit 4 mirrors silences plumbing here.
- `internal/tui/page/silences/silences.go` — existing `x` / `Ctrl+X` / `pendingExpire` machinery (~lines 797-889); commit 3 unifies these and rewires the loop.
- `internal/tui/form/silence/form.go` — Form's existing surface; commit 2 adds bulk mode.
- `internal/config/types.go:312` (Defaults) — commit 1 lands `BulkConcurrency` here.
- `internal/tui/keys/dispatch.go` — keybinding routing; commit 5 adds `Ctrl+\`.
- `cmd/tui.go` — page factories plumb config; commits 3 & 4 wire `BulkConcurrency` through.
- `docs/design/keybindings.md` — authoritative binding catalog; commit 6 reconciles.
- `docs/design/silence-write-surface.md` — predecessor doc; this series builds on its commits 2-5 (already landed).
- `derailed/k9s/internal/ui/select_table.go:46-60` — the k9s mark-fallback contract this UX mirrors.
