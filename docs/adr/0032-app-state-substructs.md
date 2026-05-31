## 0032 — App private state split into named sub-structs

`internal/tui/app.App` started life as a flat struct: by the time
an earlier comment audit landed, its private state carried eleven
fields, five of which (`modal`,
`help`, `pollCache`, `statusCache`, `histories`) each justified a
multi-paragraph doc comment. The bloat was a tell: the type was
secretly several sub-systems sharing one struct, and readers had
to keep them all live to follow `Update`, `View`, or any test that
asserted overlay state.

Two named sub-structs collapse the bloat: `overlays` (`modal` +
`help`) and `caches` (`pollCache` + `statusCache`, renamed to `poll`
and `status` inside the sub-struct since the sub-struct's own name
already says "cache"). Each sub-struct gets the rationale comment
the collection of fields already deserved; individual fields drop
to terse declarations. Call sites read `a.overlays.modal`,
`a.caches.poll` — the grouping is visible at the point of access.
`histories` stays as the existing `appHistories` struct (already
named, never bloated). `stack` stays flat — its comment is four
lines, the bloat complaint doesn't apply, and a single-field
`pageStack` wrapper would be ceremony with no readability win. The
chrome strip fields (`crumbs`, `prompt`, `flash`, `hintbar`) also
stay flat because they're a peer set queried independently by the
footer rendering path — bundling them would obscure the per-feature
test asserts that pin each strip's state.

Considered and rejected: (a) keep the flat struct, trim the
field-level docstrings — silences the comment-rule complaint but
not the underlying signal that the App owns several sub-systems.
Future readers still have to learn which fields go together.
(b) Split into sibling types the App embeds — preserves the same
`a.modal`, `a.pollCache` access shape. Rejected because the explicit
dotted access is the documentation here: `a.overlays.modal` says
*the modal is part of the overlay system* in a way embedding hides.
(c) Promote the sub-structs to their own packages — overcorrect;
these are App-internal data structures, not abstractions other code
reaches for. (d) Also wrap `stack` in `pageStack` per the audit's
literal recommendation — over-engineering for a one-field wrapper;
adopt the sub-struct shape only where there are two or more fields
to bundle.
