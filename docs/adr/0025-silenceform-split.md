# 0025 — silence-form split into a 6-file in-package layout

`internal/tui/form/silence/form.go` was the largest non-test file in
the repo at ~1180 LOC (post-ADR-0021, which extracted the matcher
parser). It conflated five distinct concerns that each have their
own contributors and review cadence: state (validation, time /
duration parsing, label-to-matcher), Bubble Tea field models (six
textinput / textarea slots, focus cycling, blur/focus routing), the
async submit lifecycle (cancellable ctx, generation token, re-entry
guard, the `mu`-guarded cancel slot the Update goroutine reads while
the worker writes it), tenant-picker integration, and rendering.
The submit lifecycle in particular carried five concurrency-shaped
invariants — Close-vs-goroutine race protection, generation-token
drop rule, parent-ctx propagation, double-Ctrl-S guard, ctx-canceled
silent drop — woven through `Form`'s field set. Exercising any one
of them required booting a full Form with theme + bubbles models +
fake clients, and the same five invariants would still need form-
shaped integration tests on top of any per-invariant unit coverage.

This ADR records the decision to **split the file into a 6-file
in-package layout** (no sub-packages, no new public types). The
files are:

- `form.go` — `Form` struct, `New`, the `app.Page` contract (`Init`,
  `Close`, `CapturesInput`, `Crumb`, `Title`, `HeaderContent`,
  `Footer`, `Bindings`, `Update`), and the `View` delegate. Owns
  orchestration only; calls into submit / state / render helpers.
- `state.go` — `parseSpec`, `parseTimeOrNow`, `parseEndsAt`,
  `formatMatchers`, `MatchersFromLabels`. Pure functions plus
  `parseSpec` on the `*Form` receiver that returns
  `backend.SilenceSpec` directly.
- `fields.go` — `fieldIndex` enum, `newInput` / `flattenTextareaBlur`
  constructors, the focus state machine (`cycleFocus`,
  `forwardToFocused`, `activeFocus`, `activeBlur`, `focusDisabled`).
- `submit.go` — the internal `submitter` value (`Start` / `Cancel` /
  `Done` / `InFlight`) that owns the cancellable-ctx wiring, the
  `gen` token, the `inFlight` flag, and the `mu`-guarded `cancel`
  slot. Form-level submit orchestration (`submitNow`,
  `applySubmitDone`, `fail`, `flashFn`) lives alongside it.
- `tenant.go` — `openTenantPicker`, `sortedTenantNames`,
  `tenantDisabled`, the `pickerOrigin` tag.
- `render.go` — the `renderView` body, `tenantRow` / `disabledRow` /
  `fieldRow` / `matcherSlotRow` / `matchersView` helpers.

The single public type stays `Form` plus its three message types
(`SubmittedMsg`, `CancelledMsg`, `BulkSubmittedMsg`) and the
`Client` interface. Everything new is lowercase: `submitter`,
`submitDoneMsg`, `fieldIndex`, `pickerOrigin`. Public surface is
byte-identical post-split.

The submitter shape is the load-bearing piece. Form holds a
`submit submitter` field (value, not pointer — the mutex is the
only concurrency-shaped field and Form is already passed by
pointer, so the value embedding adds no allocation surface).
`Start(client, id, spec)` schedules the write and returns the
`tea.Cmd`; an empty `id` picks `CreateSilence`, non-empty picks
`UpdateSilence`. `Cancel()` aborts the in-flight ctx. `Done(msg)`
clears the in-flight flag iff the message belongs to the current
generation, and reports stale otherwise. Form orchestrates: it
parses the spec (state.go), resolves the client (tenant.go), and
calls `submit.Start`. The submitter has no view onto the form, no
parsing knowledge, no field-focus knowledge — its tests construct a
zero-value `submitter` and exercise the cancellation protocol
directly without booting a Form.

Test split mirrors the production split. The full `form_test.go`
(1367 LOC) moved to: `state_test.go` (round-trip + label-drop),
`fields_test.go` (focus cycle, prefill, typing, blur), `render_test.go`
(absorbed bulk-banner / tenant-row-hint / bulk-no-tenant-row, on top
of the existing placeholder-dim / flatten-styles / disabled-row-faint
coverage), `submit_test.go` (the new isolated submitter cases:
double-Start drop, Cancel-aborts-inflight, parent-ctx-propagation,
generation bumping, stale-Done drop). `form_test.go` keeps the
orchestration tests — Update sequences that touch multiple
submodules, multi-tenant routing, edit / bulk / single-tenant
shapes, the Close-cancels-inflight integration that proves the
submitter wiring lands on the `*Form`. `fuzz_test.go` stays
untouched, per ADR-0021's judgement that the form-level fuzz target
is form-shaped and not parser-shaped.

Considered and rejected: (a) a `silenceform/state` / `silenceform/submit`
sub-package layout — would force the `Form` ↔ `submitter` boundary
through an exported type even though `submitter` is a purely
internal lifecycle helper; the in-package split preserves the
unexported surface and keeps `go doc` clean; (b) promoting `parseSpec`
output to a public domain value type (e.g. `silenceform.Spec`) — the
caller is `backend.SilenceSpec`, no second value type pulls its
weight; (c) introducing a `Submitter` interface so tests inject a
mock — the production submitter is the test subject for the
cancellation protocol, mocking it would test the mock instead;
(d) a monolithic test file kept alongside the split production
files — would leave `form_test.go` at 1367 LOC even though
state / fields / submit / render concerns now have isolated homes,
re-creating the navigation problem the ADR is meant to solve;
(e) moving `MatchersFromLabels` to `internal/matcher` next to the
parser — the function is consumed only by silenceform callers
(alerts list / alert detail / groups page) reading the
`silenceform.MatchersFromLabels` symbol; the matcher package
already declines to host a `Render` (ADR-0021's stance), and
splitting the label-projection alongside Parse would just relocate
the cohesion problem.
