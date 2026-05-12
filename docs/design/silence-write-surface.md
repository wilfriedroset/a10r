# Silence write surface: implementation plan

Status: ready to implement. Self-contained; do not replay the originating conversation to act on this.

## Goal

Finish the silence write surface so every binding in the v0.1 keybindings catalog actually works:

- **Alerts list (`s`)**: push the silence form prefilled with the cursor alert's labels as matchers.
- **Alert detail (`s`)**: same as alerts-list `s`, but operating on `p.a` (the cached alert).
- **Groups page (`s`)**: wire the existing `DrillSilenceMsg` so the wiring layer pushes the silence form prefilled with the group's common-labels intersection.
- **Silences list (`e`)**: push the silence form prefilled with the cursor silence's matchers + comment + creator + endsAt; submit via `Client.UpdateSilence`.
- **Silences list (`x`)**: confirm modal → `Client.ExpireSilence` on the cursor row.
- **Silences list (`Ctrl+X`)**: bulk version of `x` — iterate `Space`-marked rows.
- **Silences list (`Ctrl+E`)**: editor handoff via `internal/tui/edit` — round-trip the silence as YAML, apply on save via `UpdateSilence`.

Today these all land on placeholder `flashFn(footer.FlashWarn, …)` calls in `internal/tui/page/silences/silences.go:handleAction` (lines around 396-425) and `internal/tui/page/alerts/alerts.go:handleAction` (line ~459 — `case "s"`).

## Pre-implementation verifications (do these first to confirm nothing has drifted)

1. **`backend.Client` write methods exist and have not changed.** `internal/backend/client.go:38-40`:
   ```go
   CreateSilence(ctx context.Context, spec SilenceSpec) (id string, err error)
   UpdateSilence(ctx context.Context, id string, spec SilenceSpec) error
   ExpireSilence(ctx context.Context, id string) error
   ```
   Vanilla implementation: `internal/backend/vanilla/client.go` — confirm `CreateSilence` / `UpdateSilence` / `ExpireSilence` are wired against `/api/v2/silences` (POST / POST-with-id / DELETE-by-id per AM v2 OpenAPI).
2. **`form/silence` Form already implements `app.Page`** and emits `SubmittedMsg` / `CancelledMsg` that satisfy `app.AutoPopMsg` (added in commit `572672b`). Form's `Options` today: `Client`, `Styles`, `Now`, `Creator`. There is **no** option to prefill matchers/comment/endsAt — you'll add one in commit 1 of this plan.
3. **`internal/tui/edit` API**: `Resolver.Edit(Request) tea.Cmd` returns a Cmd that emits `FinishedMsg{ResourceID, Content, Err}`. `SystemResolver()` constructs the production resolver. The page receives the result via the App's catch-all `forwardToTop`.
4. **`modal.NewConfirm(question string, def ConfirmDefault) *Confirm`** emits `ConfirmResultMsg{Yes, Cancelled bool}`. The result satisfies `modal.ResultMsg` and the App auto-closes the modal then forwards the message to the parent — see `internal/tui/app/app.go` around line 258 (`isModalResult`).
5. **silences page already has** `clients map[string]silenceform.Client` and `creator string` fields (commit `572672b`). Confirm by reading `internal/tui/page/silences/silences.go:Page` struct. `pickWriteTarget()` already returns `(tenant, client, ok)`.
6. **alerts page does NOT yet have a clients map.** You'll add it in commit 2.
7. **Groups page DrillSilenceMsg has no listener in `cmd/tui.go`.** Confirm with `grep -n DrillSilenceMsg cmd/tui.go` — should return nothing.

## Settled design decisions

- **Form prefill is opt-in via new Options fields, not a separate constructor.** Adds `Matchers []backend.Matcher`, `Comment string`, `EndsAt time.Time`, `EditID string` to `silenceform.Options`. `EditID` empty → `Client.CreateSilence`; `EditID` non-empty → `Client.UpdateSilence(EditID, spec)`. The form's submit branches on EditID. This is simpler than a second form type and keeps the test surface flat.
- **Matchers prefill format**: render `name=value` (`name=~regex` for regex matchers) one per line into the `matchers` buffer. Matches the format the user types manually so editing via Tab + backspace works without a special path.
- **Tenant selection for prefill**: same rule as `n` (commit `572672b`'s `pickWriteTarget`): cursor row's tenant when a row is focused, else first in-scope tenant alphabetically. For alert-detail `s`, use the alert's `Tenant` field (already on the page).
- **`x` confirm question**: `expire silence <id>?` with `ConfirmDefaultNo` (destructive — never default-yes per project policy). Bulk `Ctrl+X` confirm: `expire <N> marked silences?` likewise default-No. No marks → flash hint, no confirm.
- **`Ctrl+E` YAML shape**: marshal the cursor silence as a small YAML doc — `id`, `comment`, `createdBy`, `startsAt` (RFC3339), `endsAt` (RFC3339), `matchers` (array of `{name, value, isRegex, isEqual}` mirroring `backend.Matcher`). On `FinishedMsg`, parse the YAML back, build a `backend.SilenceSpec`, and call `UpdateSilence(id, spec)`. Empty content (user aborted without saving) is a no-op. Non-nil `Err` flashes the error text; the silence stays unchanged.
- **No silence detail page in this series.** The silences list's `Enter` keybinding (per `keybindings.md` it's "open silence detail") stays unbound — that view is its own follow-up. `e` and `Ctrl+E` operate on the cursor row directly.
- **Silences page Bindings() drops the `Enter` chip** so the help overlay's RESOURCE column doesn't advertise an action that isn't wired yet. Re-add when the detail page lands.

## Commit sequence

Six commits. Each is independently reviewable, has its own tests, and is `prek -a` clean. Conventional-commit subjects below; expand bodies with the *why* per `CLAUDE.md`'s clean-commits feedback. After each commit, run the subagent review loop the project memory describes (general-purpose agent, "need-work / nice-to-have / nits" review).

### Commit 1 — `feat(internal/tui/form/silence): support edit-mode and prefilled matchers`

The mechanical-prep commit. Pure form-package changes; no callers.

Files:

- `internal/tui/form/silence/form.go`:
  - Add to `Options` (around line 90): `Matchers []backend.Matcher`, `Comment string`, `EndsAt time.Time`, `EditID string`. Each independently optional; doc on the type explains how they compose.
  - In `New(opts Options)` (around line 103): when `opts.Matchers` is non-empty, format them as `formatMatchers([]backend.Matcher) string` (newline-separated `name=value` / `name=~regex` / `name!=value` / `name!~regex` per `IsRegex` and `IsEqual`) and assign to `f.matchers`. When `opts.Comment` is non-empty assign to `f.comment`. When `opts.EndsAt` is non-zero, format as RFC3339 (or a compact diff like the existing `2h` shorthand if it lands cleanly within ±1m of `Now()+duration`; default to RFC3339 if uncertain). Store `opts.EditID` on the form struct.
  - Add an unexported `formatMatchers([]backend.Matcher) string` helper. Inverse of the existing `parseOneMatcher`; symmetry test asserts round-trip.
  - In `submit()` (around line 224): branch on `f.editID`. Empty → `client.CreateSilence(ctx, spec)` as today. Non-empty → `client.UpdateSilence(ctx, f.editID, spec)`; on success emit `SubmittedMsg{ID: f.editID}` so the parent's flash reads `silence updated: <id>` rather than a fresh ID.
  - Extend the `Client` interface (line 33) to include `UpdateSilence(ctx, id, spec) error`. The widened interface still narrows from `backend.Client` (which already has `UpdateSilence`) so the existing `silenceClientsFrom` adapter in `cmd/tui.go` continues to compile.
  - Title (`Title()` method, around line 127): `"new silence"` when `editID` is empty; `"edit silence " + editID` otherwise. Same one-liner branch.

- `internal/tui/form/silence/form_test.go`:
  - `TestForm_PrefillMatchers`: construct with `Options{Matchers: [...]}`. Assert `f.matchers` is the round-trip string. Cover all four operators (`=`, `!=`, `=~`, `!~`).
  - `TestForm_PrefillComment` / `TestForm_PrefillEndsAt`: trivial.
  - `TestForm_EditModeCallsUpdate`: fake client recording which method was called; assert `Update` not `Create` when `EditID` is set.
  - `TestForm_FormatMatchersRoundTrip`: parse-format-parse symmetry on a varied input.
  - `TestForm_TitleSwitchesOnEditID`.

Out of scope: no caller changes; that's commits 2-6.

### Commit 2 — `feat(internal/tui/page/alerts): wire `s` to push prefilled silence form`

Files:

- `internal/tui/page/alerts/alerts.go`:
  - Add `Clients map[string]silenceform.Client` and `Creator string` to `Options` (struct around line 75 area). Stash on the Page struct.
  - In `handleAction` (around line 459), replace the `"silence form arrives in #30"` flash with a call to a new `openSilenceFormForCursor()` method.
  - `openSilenceFormForCursor()`:
    1. Empty view → flash `no alert under the cursor`.
    2. `tenant := p.view[p.cursor].tenant`. Look up `p.clients[tenant]`. Missing → flash same hint as silences page (`no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T``).
    3. Build `[]backend.Matcher` from the alert's labels. Every entry is `IsEqual: true, IsRegex: false`. **Skip the `__name__` label** if present (synthetic, would silence everything matching the metric name).
    4. `app.PushPage(func() app.Page { return silenceform.New(silenceform.Options{Client: client, Styles: ..., Now: ..., Creator: ..., Matchers: matchers}) })`.
  - Add a `silenceform.SubmittedMsg` / `silenceform.CancelledMsg` handler in `Update` so the alerts page flashes on submit success (same `silence created: <id>` shape as silences). Cancelled is silent. Same auto-pop pattern as silences (commit `572672b`).

- `internal/tui/page/alert/alert.go`:
  - Mirror image. Add `Clients map[string]silenceform.Client` and `Creator string` to `Options`.
  - In `handleKey` (around line 130) for `case "s"`, replace the existing flash placeholder with a push of the form, prefilled with `p.a.Labels` (minus `__name__`) as matchers and `p.tenant` as the target backend.
  - Same SubmittedMsg / CancelledMsg handlers.

- `cmd/tui.go`:
  - Pass `Clients: silenceClients` and `Creator: creator` (already in scope from `newResolver`) into the `alerts.New` factories at line 88 and line 204. Same fields the silences factory already consumes.
  - `alert.New` in `internal/tui/page/alerts/alerts.go:drillToDetail` (line 501) gets `Clients` and `Creator` threaded from the alerts page's stored fields. Add fields to `alert.Options` and to the call site.

Tests:

- `internal/tui/page/alerts/alerts_test.go`:
  - `TestPage_SKeyWithoutClientsFlashesHint`: `s` with no Clients → warn flash.
  - `TestPage_SKeyPushesFormPrefilledFromAlert`: `s` with a fake Client present → cmd is a pushPageMsg; the pushed page is a `*silenceform.Form` whose `matchers` field contains the alert's labels (sans `__name__`). One way to assert: cast the result of the factory and read the unexported `matchers` field via a test-only `MatchersForTest()` method — OR assert through the rendered `View` output (`silenceform.New(...).View(120, 30)` includes the matchers buffer in plain text). Pick whichever is cleaner.
  - `TestPage_SKeyOnEmptyViewFlashesHint`: cursor past view → "no alert under the cursor".
- `internal/tui/page/alert/alert_test.go`:
  - Same three cases against `p.a.Labels`.

Out of scope: groups, silences, expire, editor.

### Commit 3 — `feat(cmd/tui): wire DrillSilenceMsg from groups to silence form`

Files:

- `cmd/tui.go`:
  - In `runTUI` (after the home-page push goroutine), spawn a small message-routing goroutine — or, better, add a top-level message in `internal/tui/app/app.go` that the App listens for. Trade-off: the App-level listener is the right pattern (matches how `tenant.SelectedMsg` and `groups.DrillAlertMsg` are handled — actually re-check, both currently land at `forwardToTop` which doesn't help when the receiver is a different page).
  - Cleanest path: add `groups.DrillSilenceMsg` handling in **the silences page is wrong (group page emits, alerts page or App should consume)**. Right home is the App's `handleLifecycle` — it intercepts `groups.DrillSilenceMsg` and pushes the silence form prefilled with the common labels as matchers, picking the tenant the same way the silences page does (cursor row's tenant from groups page if reachable, else first in-scope).
  - Actually simpler: make the **groups page** own the form push directly. It already has the cursor row's tenant (each `groupEntry` carries `tenant string`). Skip the App-level routing entirely. Add the same `Clients` / `Creator` plumbing to `groups.Options` that you added for silences/alerts, and replace `onSilence` (around `groups.go:373`) so it pushes the form directly with `Matchers: matchersFromLabels(common, false)` (skipping `__name__`).
  - Wire `groups.New` in `cmd/tui.go:newResolver` (around line 224) to receive `Clients: silenceClients` and `Creator: creator`.
  - Drop `DrillSilenceMsg` from the `groups` package — it's unused once the page pushes directly. (Or keep it for future App-level routing; either is fine. Default: drop, less surface area.)

Tests:

- `internal/tui/page/groups/groups_test.go`:
  - `TestPage_SKeyPushesFormPrefilledWithCommonLabels`: same shape as the alerts test. Group with two alerts sharing `team=platform` (and one differing label) — `s` pushes form with `team=platform` prefilled, no `alertname=`.

### Commit 4 — `feat(internal/tui/page/silences): wire `e` to edit form, `x` to expire confirm`

Files:

- `internal/tui/page/silences/silences.go`:
  - Replace the `"silence edit arrives in #30"` flash in `handleAction` with `openEditSilenceForm()` — same shape as `openNewSilenceForm()` (commit `572672b`) but builds `silenceform.Options{EditID: silence.ID, Matchers: silence.Matchers, Comment: silence.Comment, Creator: silence.CreatedBy, EndsAt: silence.EndsAt, Client: ...}`.
  - Replace the `"silence expire arrives in #30 (with confirm)"` flash with `openExpireConfirm()` that pushes a `modal.NewConfirm("expire silence "+id+"?", modal.ConfirmDefaultNo)` and stores the cursor silence ID + tenant on the page so the result handler knows which silence to expire.
  - Add a `case modal.ConfirmResultMsg` in `Update`. On `Yes && !Cancelled`, call `client.ExpireSilence(ctx, pendingExpireID)` synchronously (the call is a single HTTP DELETE; flash on success or error). On `Cancelled` or `!Yes`, drop the pending state silently.
  - `Ctrl+X` (`bulk expire`):
    - No marks → flash `no rows marked — Space marks one` (or similar; mirror what the alerts page does for bulk on empty marks).
    - With marks → confirm modal with `expire <N> marked silences?`. On Yes, iterate every marked silence ID, call `ExpireSilence`, accumulate failures, flash a summary (`expired N silences` or `expired M of N — see log for failures`).
  - Add a `marks map[string]struct{}` to the silences page (mirror alerts page) plus `Space` toggling on the cursor row + a `MARK` indicator in `padColumns` when marks exist. Reuse the alerts page's pattern verbatim.

- `internal/tui/app/app.go`:
  - **No changes** — `ConfirmResultMsg` already routes through the existing `isModalResult` path (lands at `forwardToTop`). The silences page picks it up.

Tests:

- `internal/tui/page/silences/silences_test.go`:
  - `TestPage_EKeyPushesFormPrefilledFromCursorSilence`: cursor on row 1, `e` → cmd carries pushPageMsg; the form's `matchers` / `comment` / `editID` mirror the row.
  - `TestPage_XKeyOpensConfirmModal`: `x` on the cursor → cmd carries openModalMsg with title `confirm`.
  - `TestPage_ConfirmYesCallsExpireSilence`: feed `ConfirmResultMsg{Yes: true}` after seeding the pending state; assert the fake Client's `ExpireSilence` saw the right ID.
  - `TestPage_ConfirmNoIsNoop`: feed `Yes: false`; no Client call.
  - `TestPage_ConfirmCancelledIsNoop`: feed `Cancelled: true`; no Client call.
  - `TestPage_BulkExpireRequiresMarks`: `Ctrl+X` with no marks → warn flash.
  - `TestPage_BulkExpireConfirmsAndIteratesMarks`: mark two rows, `Ctrl+X`, feed `Yes` → ExpireSilence called twice with the two marked IDs.
  - `TestPage_BulkExpireSummaryFlashesPartialFailure`: one of the marked rows' expire returns an error → flash text contains both the success count and the failure hint.

### Commit 5 — `feat(internal/tui/page/silences): wire Ctrl+E to editor handoff`

Files:

- `internal/tui/page/silences/silences.go`:
  - Add `editor edit.Resolver` to the page struct + `EditorResolver edit.Resolver` to `Options`. Default empty resolver flashes a hint (`editor handoff requires $EDITOR or $A10R_EDITOR`).
  - Replace the `"$EDITOR handoff arrives in #31"` placeholder in `handleAction` for `ctrl+e` with `openEditorForCursor()`:
    1. No row → flash `no silence under the cursor`.
    2. Marshal the cursor silence as YAML using `gopkg.in/yaml.v3` (already on the dep allowlist).
    3. `cmd := p.editor.Edit(edit.Request{ResourceID: silence.ID, Initial: yamlBytes, Extension: "yaml"})`.
    4. Stash the silence's tenant + client on the page so the `FinishedMsg` handler knows where to send the update.
  - `case edit.FinishedMsg` in `Update`:
    - `m.Err != nil` → flash error.
    - `m.Content == ""` (user aborted without saving) → silent no-op.
    - Otherwise: parse YAML → `backend.SilenceSpec` + ID; call `client.UpdateSilence(ctx, id, spec)`; flash `silence updated: <id>` on success or the error text on failure.

- `cmd/tui.go`:
  - Pass `EditorResolver: edit.SystemResolver()` into the silences factory.

- New helper file `internal/tui/page/silences/yaml.go`:
  - `func silenceToYAML(backend.Silence) ([]byte, error)` and `func silenceFromYAML([]byte) (id string, spec backend.SilenceSpec, err error)`. Internal types used for marshalling — small struct with public yaml tags. Tests round-trip a sample.

Tests:

- `internal/tui/page/silences/silences_test.go`:
  - `TestPage_CtrlEKeyEmitsEditCmd`: fake `edit.Resolver` whose `Edit` records the request. Assert the request's `ResourceID` and the YAML body parses back to the original silence.
  - `TestPage_FinishedMsgEmptyContentIsNoop`: `FinishedMsg{Content: ""}` → no Update call, no flash.
  - `TestPage_FinishedMsgErrorFlashes`: `FinishedMsg{Err: ...}` → error flash.
  - `TestPage_FinishedMsgSuccessCallsUpdateSilence`: legitimate edited YAML → fake Client's `UpdateSilence` called with the right ID + spec.
  - `TestPage_YAMLRoundTrip` (in `yaml_test.go`): symmetric `silenceToYAML` / `silenceFromYAML`.

### Commit 6 — `docs: changelog notes silence write surface`

Single-file `CHANGELOG.md` bump under the `## [v0.1.0] — TBD` section. Polish entries:

- `s` on alerts list / alert detail / groups now opens the silence form prefilled with matchers from the source resource (alert's labels minus `__name__`, group's common-labels intersection).
- `e` on silences opens the form in edit mode (calls `UpdateSilence` on submit).
- `x` on silences opens a confirm dialog and calls `ExpireSilence` on Yes; default-No so a stray Enter never destroys data. `Ctrl+X` does the same in bulk over `Space`-marked rows.
- `Ctrl+E` on silences round-trips the silence as YAML through `$EDITOR` (or `$A10R_EDITOR`). Saving applies via `UpdateSilence`; aborting without saving is a silent no-op.

No code in this commit. `prek -a` only — skip the subagent review (project policy: docs commits skip the review).

## Out of scope (deferred, named so the receiving agent doesn't scope-creep)

- **Silence detail page** (the bound `Enter` action listed in `docs/design/keybindings.md` for the silences view). Independent piece of work — a read-only view of one silence with its affected alerts. Touch only the silences page's `handleAction` when it lands; this series leaves `Enter` unbound.
- **Silence schedule UI** (start in the future, recurring patterns). The form's `starts` / `ends` text fields already accept RFC3339 / duration shorthand — power users have an escape hatch — but there's no fancy datetime picker.
- **Tenant prompt on `s` from a multi-tenant scope with no clear cursor row** (e.g. user is on the alerts list with `<0> all` selected and no row focused). Today the rule is "first in-scope tenant alphabetically" — that's deterministic and good enough. A future modal could explicitly ask. **Superseded by ADR-0011** for the silence form's `n` / `Ctrl+N` entry points: the form now renders an editable `Tenant:` field backed by `modal.Picker`. The `s` shortcut (silence-this-alert from the alerts view) is not yet routed through the same selector — it still uses the cursor-row tenant; routing it through the new form-owned picker is a follow-up.
- **Optimistic UI updates** (insert the new silence into `byTenant` immediately rather than wait for the next poll tick). The poll interval is 1 minute by default — visible delay is real but a v0.2 concern.

## Acceptance checks before merging the series

Run after each commit:

- `prek -a` clean.
- `go test -race -timeout 60s ./...` green.
- `golangci-lint run --timeout 60s ./...` clean.

Run after the whole series:

- `make build && ./a10r -c examples/demo.yaml` — `:silences`, `n`, fill the form, submit → flash success, next poll surfaces the silence.
- Same flow with `e` → form opens prefilled.
- `x` → confirm dialog → Yes → flash success → next poll the silence is gone.
- `Space Space Ctrl+X` → bulk confirm → Yes → both gone.
- `Ctrl+E` → editor opens with the YAML; `:wq` → flash success; abandon (`:q!`) → silent no-op.
- Multi-tenant smoke against your two-tenant prod config: every action above honours scope; `<1>` then `n` lands a silence on tenant 1 only.

Subagent review per the project's clean-commits feedback memory after each non-doc commit. The locked review prompt: priorities (need-work / nice-to-have / nits) × categories (maintainability / testability / scalability / golang idiomatic). Address `need-work` always; `nice-to-have` unless it expands scope; nits with judgement.

## Critical files for reference

- `internal/tui/page/silences/silences.go` — existing `n` wiring (commit `572672b`); `pickWriteTarget` is the tenant-selection helper to mirror.
- `internal/tui/page/alerts/alerts.go` — existing marks/cursor/byTenant pattern; commit 2 mirrors these for the alerts-list `s`.
- `internal/tui/form/silence/form.go` — Form's existing surface; commit 1 extends it.
- `internal/tui/edit/edit.go` — editor handoff; `Resolver.Edit` returns the `tea.Cmd`.
- `internal/tui/modal/confirm.go` — confirm modal; `ConfirmResultMsg{Yes, Cancelled}` is the result shape.
- `internal/backend/client.go:38-40` — write methods on the client interface; vanilla and Mimir implementations.
- `cmd/tui.go:newResolver` — every page factory plumbs through here; commits 2/3/5 add `Clients` / `Creator` / `EditorResolver` arguments.
- `docs/design/keybindings.md` — authoritative binding list; cross-check each new wire-up against the catalog.
