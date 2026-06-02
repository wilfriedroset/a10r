# 0022 — detail-page shared base

The three detail pages (`internal/tui/page/{alert,silence,tenantconfig}`)
each carry their own copies of the same 1D-scroll skeleton: an `int`
scroll offset plus an `int` body-height snapshot, a switch over
`j`/`k`/`G`/`Ctrl+D`/`Ctrl+U`/`Ctrl+F`/`Ctrl+B` that walks the offset
through `cursor.HalfPageStep` / `cursor.FullPageStep`, an
`app.GoToFirstRowMsg` case that resets the offset to zero, a clamp
pass at the top of `View` that pins `scroll ∈ [0, max(lines-height,
0)]`, and the four no-op `app.Page` shapes (`Init`, `Close`,
`HeaderContent`, `Footer`) that not every detail page overrides.
The list pages already share `listpage.Base` (ADR-0013) for the same
shape of duplication on the table side; the detail pages did not have
an equivalent. The three-caller threshold the listpage rule uses for
inclusion is met here for the scroll skeleton and the sideband
dispatch — `alert`, `silence`, and `tenantconfig` each carry the
same code byte-for-byte modulo identifier renames.

This ADR introduces `internal/tui/page/detailpage/` to hold a `Base`
struct (the `Scroll` / `BodyHeight` pair plus the optional callback
fields) and the shared helpers: `HandleScrollKey(key string) (handled
bool)` for the seven vim motions, `ReconcileScroll(totalLines, height
int)` for the View-time clamp, and `HandleSidebandMsg(msg) (handled
bool, cmd tea.Cmd)` for the cross-cutting messages. Each page embeds
`*Base` (pointer, so constructor wiring of closure-style callbacks
can reference the page itself without circular-init headaches), wires
the optional hooks it cares about at construction, and calls the
helpers from its own `Update`/`View`. `Base` deliberately does **not**
implement `tea.Model` — the `Update`/`View` dispatch order differs
per page (alert handles `poll.DataMsg`/`silenceform` results before
keys; tenantconfig handles `statusFetchedMsg` first; silence has the
shortest path) and an `Update`-shaped method on `Base` would either
freeze that order or push the per-page concerns into Base, both
worse than the explicit call sites. `Init`, `Close`,
`HeaderContent`, and `Footer` get default no-op implementations on
`Base` so pages that don't care about them stop carrying their own
nil-returning copies; pages that *do* care (tenantconfig's lazy
status fetch, its "fetching…" header) override directly.

The discipline for the sideband router mirrors ADR-0018: universal
cases panic on missing dependencies, optional cases fall through.
For detail pages today the only universal case is
`app.GoToFirstRowMsg`, which operates on `Scroll` directly with no
callback — no panic surface exists because nothing can be nil. The
optional cases — `app.TimeFormatChangedMsg` (only alert renders
relative timestamps) and `modal.ResultMsg` (only alert opens modals
via the silenced-by picker) — treat a nil `SetTimeFormat` /
`OnModalResult` as `handled=false` and fall through to the page's
main switch. `InitCmd` is the third optional hook: tenantconfig's
`/api/v2/status` fetch returns a Cmd from `Init`; alert and silence
have nil Inits today and `Base.Init` returns nil when `InitCmd` is
unwired.

`alert.Clipboard` and `alert.Browser` stay on `alert.Page`. Rule-of-
three says they do not belong on Base — silence and tenantconfig have
no copy-to-clipboard or open-URL affordance today. Lifting them would
saddle two pages with nil-checked indirection for a feature they
never use; the alert page is the only place either interface has a
honest call site.

Considered and rejected: (a) routing detail-page scroll through
`cursor.Window` — Window models a row cursor + topRow pair (ADR-0016)
that detail pages don't have; the scroll offset here is single-axis
and shrinking it into Window would require a "topRow-only mode" that
fights Window's contract; (b) making `Base` implement `tea.Model`
with a default Update that handles the sideband and falls through to
a page-provided handler interface — contradicts ADR-0013's load-
bearing "Base does not implement tea.Model" rule, which is what keeps
per-page dispatch order explicit and testable; (c) lifting
`alert.Clipboard` / `alert.Browser` interfaces onto `Base` as
nilable hooks "in case a future page wants them" — speculative
abstraction over actual code today, and the rule-of-three test fails
(one caller, not three); (d) extracting only two pages' worth of
overlap and leaving the third inline — the three-detail-page surface
is the abstraction's reason to exist; pulling Base out for two
callers means we'd revisit the decision the moment a third lands,
and the third caller is already here; (e) folding the modal-result
dispatch into a free function `detailpage.RouteModalResult` taking a
typed handler — the field-on-Base shape matches `Recompute` /
`SetTimeFormat` on `listpage.Base` and reads consistently at the
constructor; a free function would create a parallel dispatch
surface for no semantic gain.
