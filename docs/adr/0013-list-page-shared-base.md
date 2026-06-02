# 0013 — list-page shared base

The four list pages (`internal/tui/page/{alerts,silences,groups,receivers}`)
each carry their own copies of seven helpers (`scopeIncludes`,
`knownTenant`, `renderErrorBand`, `handleFilterPrompt`,
`showTenantColumn`, cursor clamping, scope-change handling) plus a
backbone of nine type-independent fields (`cursor`, `topRow`,
`bodyHeight`, `filter`, `preFilter`, `scope`, `paused`, `lastErrors`,
`tenants`). Three of the four also carry a five-field polling-UI
block (`refreshing`, `pausedRefresh`, `polledTenants`, `nextRefresh`,
`spinner`) that `receivers` does not. A prior page-duplication audit
catalogued ~600–700 LOC of duplication outside bulk and ~800 LOC
inside the two bulk implementations.

This ADR introduces `internal/tui/page/listpage/` to hold a `Base`
struct (the nine universal fields, embedded by all four pages) and a
`PollingUI` struct (the five polling fields, embedded by the three
polled pages — `receivers` embeds only `Base` because it has no
manual refresh, no spinner-during-refresh UI, and no per-tenant
refresh display). `Base` exposes the universal helpers as methods
and a `Recompute func()` callback that pages wire at construction so
`Base.HandleScopeChangedMsg` can trigger per-page recompute without
inverting control. `Base` deliberately does **not** implement
`tea.Model` — each page keeps its own `Update`/`View`/`Init` and
calls into `Base` explicitly, so the embedding does not drift into
a framework-inside-the-framework. The inclusion rule for `listpage`
is strict: code enters only when used by three or more list pages
today, never imports a concrete page package, never switches on a
page kind, and never implements `tea.Model`. The cursor-related
methods (`Base.ClampCursor`, `Base.ReconcileScroll`) wrap the
existing pure functions in `internal/tui/page/cursor/`, preserving
the stateless contract of that package — `Base` adds the field-
mutation glue, the pure functions stay independently testable, and
the `tenant/` page (which does not embed `Base`) keeps calling the
pure functions directly.

Bulk dedup lives in a separate package, `internal/tui/bulkop`, with a
generic `BulkOp[K comparable]` parameterised by key type and per-
tenant write callable. It is explicitly framed as a two-caller dedup
(`alerts/bulk.go` and `silences/bulk.go`) and the package boundary
documents that it is **not** subject to the listpage rule of three.
The separation matters: if a third bulk page lands it joins
`bulkop`; if the third never arrives the duplication stays bounded
at two callers inside one package, instead of leaking the bulk
machinery into `listpage` where it would mislead future readers
about which abstractions are universal.

Considered and rejected: (a) an interface-and-free-function approach
instead of struct embedding — the seven helpers read and write shared
state across ~14 fields, so the interface variant would require
getter and setter methods for each field on every page, relocating
the duplication into accessor boilerplate without reducing it; (b)
generic `Base[T any, E any]` to absorb the type-dependent fields
(`byTenant`, `view`, `sorter`) — the algorithms that read those
fields (recompute, sort-by-column, format-row) stay per-page, so
generics would have shared the storage without sharing the logic,
producing a half-abstraction with compile-error noise at every embed
site; (c) lifting `handleKey`/`handleMotion`/`handleSort` wrappers
into `Base` — dispatch ordering and motion semantics differ across
the four pages in ways that encode genuine per-page intent
(alerts/silences use motion → sort → action; groups has expand/
collapse; receivers has single-axis sort that eats h/l keys), so the
wrappers are page-specific glue, not duplicated behaviour; (d)
unifying the empty-state strings — per-page tone (e.g. alerts'
"the poller will refresh on the next tick", absent from groups) is
intentional micro-personality, not drift; the only real drift
(missing "— Esc clears the prompt" suffix on silences and
receivers) is fixed as a one-line patch per page, not an
abstraction.
