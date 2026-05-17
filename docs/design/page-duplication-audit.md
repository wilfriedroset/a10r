# Page packages: duplication and discrepancy audit

Status: findings final; design captured in [ADR 0013](../adr/0013-list-page-shared-base.md). This file remains the canonical
finding-by-finding record; the ADR is the canonical decision record. Patches below incorporate the corrections that surfaced during design grilling.

## Scope

Audit of `internal/tui/page/{alerts,silences,groups,receivers,tenant,tenantconfig,alert,silence,status}` for code that has drifted into copy-paste across list pages, and for behavioural inconsistencies between pages that look unintentional.

Methodology: structural read of every page package + spot-checks against the
source. Every claim below is anchored to `file:line`.

## Verified duplication

### Helpers with identical (or near-identical) bodies

| Helper | Copies | Locations |
|---|---|---|
| `scopeIncludes(tenant string) bool` | 3 identical bodies | `alerts/state.go:60`, `silences/state.go:43`, `groups/state.go:38`. A fourth variant on a different field exists at `tenant/tenant.go:503`. |
| `knownTenant(name string) bool` | 4 | `alerts/state.go:171`, `silences/state.go:32`, `groups/state.go:30`, `receivers/receivers.go:168` |
| `nextRefreshLabel(now, next)` | 3 | `alerts/alerts.go:540`, `silences/silences.go:558`, `groups/groups.go:400` (receivers has no refresh display) |
| `renderErrorBand(width int)` | **4** | `alerts/render.go:59`, `silences/render.go:57`, `groups/render.go:118`, `receivers/receivers.go:593` |
| `showTenantColumn() bool` | 3 | `alerts/state.go:44`, `silences/state.go`, `groups/state.go:239` (receivers does not gate on tenant column) |
| `handleFilterPrompt(msg)` | **4** | `alerts/handlers.go:166`, `silences/handlers.go:180`, `groups/handlers.go:106`, `receivers/receivers.go:383`. The comment at `silences/handlers.go:176` literally says *"mirrors the alerts page's handler"*. |
| `handleSidebandMsg(msg)` | 2 extracted helpers | Extracted in `alerts/handlers.go:132` and `silences/handlers.go:156`. Groups handles the applicable sideband messages inline at `groups/handlers.go:73-81` (`ScopeChangedMsg`, `GoToFirstRowMsg`); receivers does the same inline (`TimeFormatChangedMsg` and `ClearMarksMsg` are inapplicable to both — no time columns, no marks). See Discrepancies for the corrected framing. |

### Sibling files with mirrored structure

- `alerts/bulk.go` (435 LOC) and `silences/bulk.go` (388 LOC). Same pending-state struct, same unified-open routing on mark count, same per-tenant fan-out, near-identical modal/confirmation wording. The two differ in:
  - key type (fingerprint vs ID)
  - final per-tenant write call (silence create vs silence expire)
  - per-failure slog detail

### Repeated `Page` struct fields

Every list page redeclares the same backbone of fields. Cross-page recurrence verified at:

- alerts: `internal/tui/page/alerts/alerts.go:200-266`
- silences: `internal/tui/page/silences/silences.go:118-145`
- groups: `internal/tui/page/groups/groups.go:157-201`
- receivers: `internal/tui/page/receivers/receivers.go:89-121`

Recurring fields:

- Layout/cursor: `cursor`, `topRow`, `bodyHeight`, `view`
- Filter: `filter`, `preFilter`
- Scope/data: `scope`, `byTenant`, `polledTenants`
- Polling: `nextRefresh`, `refreshing`, `paused`, `pausedRefresh`, `lastErrors`, `spinner`
- Sort: `sorter` (plus a focus-tracking field that varies in name: `focusFingerprint`, `focusID`, `focusKey`, `focusName`)
- Tenant-aware: `readOnly`, `tenants`, `bulkCtx`, `submitCtx`

## Discrepancies (not pure duplication)

1. **Structural variance in sideband dispatch, not a behavioural gap.** The original draft of this audit claimed groups was "missing `handleSidebandMsg` entirely." That was wrong. Groups handles `app.ScopeChangedMsg` and `app.GoToFirstRowMsg` inline in `Update` at `groups/handlers.go:73-81`; receivers does the same. The other two sideband messages (`app.TimeFormatChangedMsg`, `app.ClearMarksMsg`) are inapplicable to both groups and receivers (no time-formatted columns, no marks). What actually differs is the *dispatch shape*: alerts and silences extract a `handleSidebandMsg` helper to stay under the cyclop budget; groups and receivers handle the cases inline. ADR 0013 standardises on a single dispatcher (`Base.HandleScopeChangedMsg` with a `Recompute func()` callback) for the one universal-state-mutating sideband message (`ScopeChangedMsg`); `GoToFirstRowMsg` stays per-page because it needs `len(view)` plus a page-typed `snapshotFocus`; `TimeFormatChangedMsg` and `ClearMarksMsg` stay per-page (two callers, below the rule-of-three threshold). Future universal sideband messages drop into `Base.HandleSidebandMsg` once they meet the threshold.
2. **Scroll reconciliation differs, not cursor clamping.** The original draft of this audit claimed alerts/silences clamp the cursor after focus lookup while groups clamps via a deferred call. Re-reading the code shows all three pages share the same logical pattern (focus lookup → if hit, set cursor and return; else clamp; else snapshot focus). The real divergence is the **scroll reconciliation step**: alerts (`state.go:135`) and silences (`state.go:120`) do *not* call `recomputeScroll` inside `recompute`'s focus-restore path — they rely on callers to reconcile scroll after `recompute` returns. Groups (`state.go:57`) defers `recomputeScroll` unconditionally, so scroll always reconciles. The alerts/silences pattern is a latent bug: when focus restoration moves the cursor to a row outside the current top/bottom window, `topRow` is not updated, so the cursor lands off-screen until the next event triggers a scroll reconcile. ADR 0013 standardises on the groups pattern (always-reconcile-on-recompute) and closes the latent bug as a side effect.
3. **Empty-state copy drift is partly intentional, partly real.** Per-page tone (e.g. alerts' filter-active hint mentions `Shift+F state filters` because only alerts has state filters; alerts' cold-start hint mentions the poller because alerts is the most-watched page) is a deliberate stylistic choice and stays per-page. The actual drift is narrow: silences and receivers omit the `— Esc clears the prompt` suffix on the filter-active branch that alerts and groups carry. ADR 0013 fixes this as a one-line patch per page; the rest stays as-is.
4. **Receivers has no `pausedRefresh` one-shot.** This is intentional and is the same property that makes receivers embed `listpage.Base` only, not `listpage.PollingUI`: receivers has no manual `r` refresh, so the pausedRefresh escape hatch does not apply. ADR 0013 formalises the asymmetry by splitting the polling-UI block into its own optional embedded substruct.

## Refactor proposals, in leverage order

The design these proposals describe is the one ADR 0013 records. The proposals are kept here in their corrected form so the leverage breakdown and risk notes remain auditable; the ADR is the canonical decision document.

### 1. Extract `internal/tui/page/listpage` (was `pagebase` in the draft)

Create `internal/tui/page/listpage/` (sibling of `cursor/` and `format/` under `internal/tui/page/`). Hold a `Base` struct with the nine universal fields (`cursor`, `topRow`, `bodyHeight`, `filter`, `preFilter`, `scope`, `paused`, `lastErrors`, `tenants`) and a `Recompute func()` callback wired by each page at construction. Expose the universal helpers as methods on `Base`: `ScopeIncludes`, `KnownTenant`, `RenderErrorBand`, `HandleFilterPrompt` (4-of-4), `ShowTenantColumn` (3-of-4, receivers ignores), `ClampCursor(itemCount int)` and `ReconcileScroll(itemCount int)` (both wrap pure functions in `internal/tui/page/cursor/`), and `HandleScopeChangedMsg` (calls the `Recompute` callback). Each list page embeds `listpage.Base`.

Inclusion rule for `listpage` is strict — code enters only if it is (a) used by 3+ list pages today, (b) does not import a concrete page package, (c) does not implement `tea.Model`, (d) does not switch on a page kind. Rule of three is enforced; helpers used by only 2 pages stay duplicated until a third caller appears.

- Removes ~400–500 LOC of duplication.
- Tests for the lifted helpers consolidate into `listpage/`; per-page tests keep only integration coverage (invocation timing).
- Risk: low — the bodies are byte-identical today; embedding-shadowing trick keeps each migration commit green.

### 2. Lift the polling-UI block into `listpage.PollingUI`

A second substruct, `listpage.PollingUI`, holds the five polling-UI fields (`refreshing`, `pausedRefresh`, `polledTenants`, `nextRefresh`, `spinner`) and exposes `NextRefreshLabel(now time.Time) string`. Embedded by alerts/silences/groups; **not** embedded by receivers (receivers has no manual refresh, no spinner-during-refresh, no per-tenant refresh display — formalising audit discrepancy 4). The `readOnly` field stays per-page on alerts/silences/groups (1 line × 3 pages, below the worry threshold).

Cursor clamping and scroll reconciliation become structural via `Base.ClampCursor` / `Base.ReconcileScroll`. The latent off-screen-cursor bug (discrepancy 2) closes as a side effect because all pages adopt the always-reconcile-on-recompute pattern that groups already uses.

- Removes ~150–200 LOC.
- Type-dependent fields (`byTenant`, `view`, `sorter`, focus-key) stay per-page; `Base` is non-generic.
- Risk: medium — requires touching `Update` flows in every list page; mitigated by per-helper commits and the embedding-shadowing trick.

### 3. Generic `BulkOp[K comparable]` in a separate `internal/tui/bulkop` package

Collapse `alerts/bulk.go` and `silences/bulk.go` by extracting a `BulkOp[K comparable]` generic type into a new package `internal/tui/bulkop`, parameterised by:

- key type (`K`)
- per-tenant write callable
- modal/confirm copy

Two thin adapters remain in `alerts/` and `silences/`. The package is **deliberately separate from `listpage`** — bulk is a two-caller dedup today, and the listpage rule of three would otherwise forbid the extraction. Keeping bulk in its own package documents the two-caller nature explicitly and prevents the bulk machinery from leaking into list-page-universal abstractions.

- Removes ~400–500 LOC.
- Risk: medium — bulk paths are user-visible; sequenced after proposals 1 and 2 land so the structural work is stable before the bulk diff is isolated from cursor/scope/sideband churn.

### 4. Align `handleKey` / `handleMotion` / `handleSort` wrappers — **rejected**

The original draft proposed lifting the three-line dispatch wrappers to `listpage.Base`. On inspection these wrappers vary in ways that encode genuine per-page intent, not duplication:

- `handleKey` dispatch order: alerts/silences run `handleMotion` → `handleSort` → `handleAction`; groups and receivers run `handleSort` first because their motion model differs (groups has expand/collapse, receivers has no h/l motion).
- `handleMotion` exists only on alerts/silences (groups uses expand/collapse semantics; receivers has single-axis sort).
- `handleSort` on receivers eats `h`/`l`/`left`/`right` as no-ops because there is only one sortable column.

The actual shared logic is already extracted into `internal/tui/page/cursor/` (`HandleMotion`, `HandleSort`, `ReconcileScroll`, motion-step helpers). What remains in each page's `handlers.go` is page-specific glue calling those helpers with page-typed arguments. Lifting the glue layer would require either configuration-driven dispatch (violates the no-kind-switch rule) or callback-passing that pushes the variation into wiring without removing it. Skip.

Total achievable reduction across proposals 1–3: ~1,000–1,200 LOC with no functional change (plus the latent scroll-reconciliation bug closed as a side effect of proposal 2 and the Esc-hint drift closed by a one-line patch per page in silences and receivers).

## Migration strategy

The work runs on a single feature branch with multiple atomic commits — no PR boundaries. Each commit is bisectable: it compiles, `prek -a` passes, and the integration tests for every page still pass.

The key enabling technique is **embedding-shadowing**: when a page embeds `listpage.Base` and *also* has its own copy of a method, Go resolves the page's own method first. So a helper can be migrated in two commits without ever leaving the tree in a broken state:

1. Add the method to `Base` with its own unit tests; embed `Base` into the page. The page still has its own copy; promotion is shadowed. No behaviour change.
2. Delete the page's copy. Promotion now resolves to `Base`. Tests prove the swap is invariant.

For atomicity, the two steps are often combined into a single commit that adds the `Base` method, deletes the per-page copies on all 4 (or 3) pages, and consolidates the tests.

**Commit cluster ordering:**

0. Precursor: add `cursor.Clamp` pure function to `internal/tui/page/cursor/` (fills a real gap — clamp is currently inlined in alerts/silences and a private method in groups).
1. Create `listpage/` skeleton with `Base` (fields only, no methods); embed in all 4 pages; drop the shadowed field copies one substruct at a time.
2. Lift helpers in order pure → mutating → callback: `ScopeIncludes`, `KnownTenant`, `RenderErrorBand`, `ShowTenantColumn`, `HandleFilterPrompt`, `ClampCursor`, `ReconcileScroll`, then `HandleScopeChangedMsg` with the `Recompute` callback wiring.
3. Add `PollingUI` substruct; embed in alerts/silences/groups; drop the shadowed field copies; lift `NextRefreshLabel`.
4. Drift-fix commit: add `"— Esc clears the prompt"` to silences/receivers' filter-active empty-state branches.
5. Create `internal/tui/bulkop/` with `BulkOp[K comparable]`; migrate alerts and silences to it.

## Open questions for refinement

All five resolved during design grilling; see ADR 0013 for the canonical record.

- **Location:** `internal/tui/page/listpage/` (sibling of `cursor/` and `format/` under `page/`). Rejected the top-level `internal/tui/listpage/` option because it would have imported `internal/tui/page/cursor/` peer-into-sibling, a directional-dependency smell.
- **Groups sideband gap:** there is no gap. Groups handles the applicable sideband messages inline (`ScopeChangedMsg`, `GoToFirstRowMsg`); the other two are inapplicable. The audit's original framing was overstated — the difference is structural (dispatcher vs inline), not behavioural. See revised discrepancy 1 above.
- **Cursor clamping order:** the audit misdiagnosed this — clamp order is identical across all three pages. The real divergence is scroll reconciliation. Standardising on groups' always-reconcile pattern (via `Base.ReconcileScroll`) closes a latent off-screen-cursor bug in alerts/silences. See revised discrepancy 2 above.
- **Empty-state copy:** per-page tone is intentional and stays. Only the missing `— Esc clears the prompt` suffix on silences and receivers is real drift; fixed in proposal step 4 of the migration strategy.
- **Bulk roadmap:** stays at two callers (alerts and silences) for the foreseeable future. The dedup is worth doing at two callers because of the LOC scale (~800 LOC of mirrored code), but is segregated into `internal/tui/bulkop/` to document the two-caller framing explicitly and avoid leaking bulk-shaped abstractions into list-page-universal code.

## Out of scope

- `alert/alert.go` and `silence/silence.go` (detail pages). They share scroll handling but the asymmetry in capability (alert: copy/browser/silence-form/picker; silence: read-only) is intentional. Revisit only if a third detail page lands.
- `status/`, `tenantconfig/`, `tenant/` pages. Each is a special-purpose page; little overlap with the list-page cluster.
