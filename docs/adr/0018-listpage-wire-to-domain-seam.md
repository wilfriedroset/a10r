# 0018 — `listpage.Base` owns the wire→domain seam for sideband and `DataMsg`

ADR-0014 codified `Base.HandleBackendStatusMsg` as the wire→domain
seam for the failure-path **backend health** signal, collapsing
four byte-identical `case poll.BackendStatusMsg:` handlers into one
call. The four list pages still carry two siblings of the same
shape: a four-case `handleSidebandMsg` switch (`ScopeChangedMsg`,
`TimeFormatChangedMsg`, `GoToFirstRowMsg`, `ClearMarksMsg`) which
exists as a helper on `alerts` and `silences` and inline in
`groups` and `receivers`, and the success-path `poll.DataMsg`
ritual which gates the snapshot on `KnownTenant`/`Paused`/
`PausedRefresh`, captures `NextRefresh` and `PolledTenants`, clears
`Refreshing` for in-scope replies, then triggers `Recompute`. The
sideband switch is structurally identical across the four pages;
the `DataMsg` ritual is byte-identical across `alerts`, `silences`,
and `groups` (modulo the resource payload type), with `receivers`
holding a deliberately different shape per ADR-0013's no-`PollingUI`
split. This ADR introduces two new entry points to keep the
wire→domain seam in one place: `Base.HandleSidebandMsg(msg)
(handled bool, cmd tea.Cmd)` for the four app-level cross-cutting
messages, and `listpage.ApplyDataMsg[R any](b *Base, u *PollingUI,
msg poll.DataMsg, store func(tenant string, payload R)) bool` for
the success-path ritual. Pages' `Update` shrinks to a top-level
`if handled, cmd := p.HandleSidebandMsg(msg); handled { return p,
cmd }`, a `case poll.DataMsg:` that calls `ApplyDataMsg` with a
type-asserted store closure, and the page-specific cases
(`spinner.TickMsg`, write-action results, key dispatch) that
genuinely differ per page.

The sideband router uses nilable callback fields on `Base` —
`RowCount func() int`, `SnapshotFocus func()`, `SetTimeFormat
func(timerender.Format)`, `ClearMarks func() tea.Cmd` — wired by
each page at construction in the same shape as the existing
`Recompute func()`. The discipline is split by case: cases every
list page handles today (`ScopeChangedMsg` — universal, internally
delegates to `HandleScopeChangedMsg`; `GoToFirstRowMsg` — universal,
needs `RowCount` + `SnapshotFocus`) **panic on nil callback** when
the message arrives, because a silently-skipped scope change or
go-to-first-row would lose user intent without any observable
failure. Cases only some pages handle today (`TimeFormatChangedMsg`
on the time-rendering pages; `ClearMarksMsg` on the marks-having
pages) treat a nil callback as `handled=false` and fall through
to the page's main switch, which mirrors today's groups/receivers
no-op for those messages exactly. `ApplyDataMsg` follows the same
panic discipline: a nil `Base.Recompute` when a `DataMsg` arrives
is a wiring bug we want loud, not a quiet skip that leaves the
table stale. The free-function form is forced by Go — methods cannot
be generic, and the genericity over the resource payload type `R`
is the load-bearing piece (alerts store `[]backend.Alert`, silences
`[]backend.Silence`, groups `[]backend.AlertGroup`). The free
function takes `*Base` and `*PollingUI` explicitly so the seam is
visible at the call site rather than hidden behind embedding.

`receivers` stays inline. Its ritual differs in three load-bearing
ways: no `PollingUI` embed per ADR-0013 (no manual refresh, no
spinner, no per-tenant `NextRefresh`); a simpler pause gate (no
`PausedRefresh` escape hatch because there is no `r` to start one);
a payload transformation step (`[]backend.Receiver` → sorted
`[]string`) that happens *before* the `byTenant` store. Forcing
`receivers` through `ApplyDataMsg` would require either a no-op
sentinel `PollingUI` that contradicts ADR-0013, or branching inside
the helper for the transform — both worse than the seven inline
lines `receivers` carries today.

Considered and rejected: (a) lifting only the per-case methods onto
`Base` (`HandleGoToFirstRow(n int)`, `HandleTimeFormat(f
timerender.Format)`, etc.) and keeping the switch in each page —
the duplication today is the switch skeleton plus the case
boilerplate, not the case bodies; lifting only the bodies leaves
four identical switches and ignores the cross-cutting "is this a
sideband?" question; (b) lifting an `Update`-shaped method onto
`Base` that subsumes all routing — contradicts ADR-0013's "`Base`
does not implement `tea.Model`" decision, which is load-bearing for
keeping the per-page dispatch ordering (motion → sort → action vs
expand/collapse vs single-axis) explicit and testable; (c) treating
a nil `Recompute` as a silent no-op so unwired pages "still work" —
a page that forgot to wire `Recompute` would receive `DataMsg`,
update `byTenant`, and never re-render, with no test or runtime
signal; loud panic on first ingest is the honest failure mode;
(d) routing `receivers` through `ApplyDataMsg` via a sentinel
`PollingUI` — fights ADR-0013 to optimise the wrong axis ("all four
pages look the same") over the actual shape ("receivers genuinely
has a different polling story"); (e) storing the sideband
callbacks in a separate `SidebandOpts` struct passed at each call
site — verbose at the call site for no semantic gain over the
field-on-`Base` precedent set by `Recompute`.
