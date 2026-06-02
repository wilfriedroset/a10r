# 0017 — `BackendHealth` stays as a raw map on `listpage.Base`

ADR-0014 introduced `Base.BackendHealth
map[string]BackendHealth` populated by
`Base.HandleBackendStatusMsg` and consumed by `Base.ErrorBand`
to render the per-tenant **error band** (CONTEXT.md). The
raw-map shape — exported field, no encapsulating type beyond
the per-entry `BackendHealth` struct — has the smell of a
custodian waiting to happen: a future caller could bypass the
handler and write the map directly, and the entries-only-while-
not-connected invariant lives in a comment rather than in the
type. A natural deepening would introduce a `TenantHealth`
custodian (`Record`, `Recover`, `InScope`) that hides the map
and collocates the scope-filtered query with the storage.

This ADR records the decision **not** to make that change
today. The deletion test fails on the proposed custodian: there
is exactly one writer (`HandleBackendStatusMsg`) and one reader
(`ErrorBand`), both already funnel-encapsulated on `Base`.
Mentally deleting `TenantHealth` would not scatter complexity
across N callers — the upsert-or-delete logic stays inside the
existing handler and the scope-filter + sort + format stays
inside `ErrorBand`. The smell is aesthetic; the invariant is
enforced by single-writer convention. ADR-0014's wire→domain
split at the `Base` seam is the load-bearing piece, and it is
already in place.

Future triggers that would flip this decision: a second writer
on the map (e.g., a "clear all unhealthy entries on scope
change" rule, or a doctor-command poke), or a second non-error-
band reader (header tooltip and doctor command are named in
ADR-0014 as candidates). Either arrival turns the funnel-of-one
into a funnel-of-two, and the type-level enforcement starts
earning its keep. Until then, exported map plus single-writer
handler is the honest shape.

Considered and rejected: (a) `TenantHealth` custodian today —
fails the deletion test, see above; (b) hiding the field behind
a lowercase rename without a custodian — would relocate the
`BackendHealth: map[string]listpage.BackendHealth{}` literal in
each of the four page constructors into a factory call, breaking
four sites for zero depth gain; (c) making the per-entry
`BackendHealth` struct unexported — it crosses the package
boundary as the value type the wire-format handler writes, and
lowering it would only push the map-literal's value-type
spelling somewhere else.
