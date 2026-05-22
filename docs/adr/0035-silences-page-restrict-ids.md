# 0035 — Multi-silence drill-in from alert-detail is the silences page, restricted

The alert-detail `S` binding had three branches by silenced-by
count: 0 flashes "no silences attached", 1 pushes silence-detail
direct, and 2+ opened a fuzzy-match modal picker
(`silencepicker.SilencePicker`) listing each silence as a single
rendered line `<id>  expires in 2h  by alice  — comment`. The
modal was a single-column, no-sort, no-filter, no-state-glyph
surface — fine for picking from a flat list, hostile when the
operator wanted the columnar layout / sort / cursor-state-tint /
expired-dim affordances they already use daily on the silences
list page. Reported as "the UX is not great, I was expecting the
same view as the silences page."

This ADR records the decision to **drop the modal kind** and have
the N-silence branch push the silences list page restricted to
the alert's silenced-by IDs. `silences.Options` grows three
optional fields — `RestrictIDs []string`, `AlertName string`,
`AlertLabels map[string]string` — and the page wires them into
`recompute` (snapshot is filtered to the ID set when `RestrictIDs`
is non-empty), `Title()` (substitutes `AlertName` for the scope
label so the title reads `silences(<alertname>)[N]` instead of
`silences(<scope>)[N]`), and the `n` handler (prefills the
silence form's matchers from `AlertLabels` via
`silenceform.MatchersFromLabels`, matching alert-detail `s`).
The 0/1 shortcuts are unchanged: zero still flashes the soft hint,
one still pushes silence-detail direct — the user gets the
columnar list only when there's actually a choice to make. The
restriction is **frozen at push time** because the only honest
source of "which silences silence this alert" is the same Alert
snapshot the alert-detail page is rendering; re-evaluating
client-side as the silences feed updates would mean re-implementing
server-side matcher semantics, and that's out of scope.

The cheaper alternative — keep the modal but render rich columns
inside its viewport — was rejected because a modal panel is
visually cramped, and the operator's mental model is already
"the silences page does this". The bigger alternative — extract a
shared "silences table" component, with the silences page and a
new alert-silences page both consuming it — was rejected for now:
the only divergence between the two surfaces is one filter step, a
title-scope substitution, and a different `n` prefill, all of
which fit cleanly behind three optional fields. Extraction would
churn every test under `internal/tui/page/silences/` to chase the
moved code, and the gain (a second page that shares the table) is
hypothetical until a third caller appears. We can re-extract when
that happens; the public surface (`silences.Options`) is the seam.

The page identity divergence (title says `silences(<alertname>)`
but crumb stays `silences`) is deliberate: the breadcrumb stack
already disambiguates by depth (`alerts > detail > silences` vs.
`silences` alone), and the alertname in the title is the load-
bearing signal that the page is restricted. Splitting the crumb
into a second term would multiply the vocabulary the operator has
to track for one feature.

`n` prefilling from `AlertLabels` on the restricted view diverges
from the regular silences page's blank `n` because the restricted
view is contextually scoped to one alert — "create another
silence" from this surface plausibly means "another silence for
this alert", and forcing the operator to retype the matchers when
they pressed `n` two pages deep into an alert reads as a
regression. The alert-detail `s` binding still does the same
prefill; the operator now has two equivalent entry points to the
same prefilled form (Esc back and press `s`, or stay on the
restricted view and press `n`), which is the desired ergonomics.

Considered and rejected: (a) **rich modal** — column layout +
cursor + expired-dim inside the existing modal panel; cheapest
delta and leaves ADR 0020's modal-kinds taxonomy intact, but the
modal frame is visually cramped and doesn't carry sort / filter /
write verbs the operator expects; the parity request was explicit;
(b) **extract a shared silences-table component** — cleaner
architectural separation, but real refactor cost (every test
under `silences/` moves) for a benefit that materialises only when
a third caller exists; the option-fields approach keeps the door
open for that extraction later, with `silences.Options` as the
seam; (c) **embed `silences.Page` inside a wrapper page** —
smallest change to `silences.go`, but the wrapper has to forward
`Bindings()`, `TimeFormat`, modal-result handling, polling, and
write-action plumbing one-for-one; the option-fields approach
trades three optional fields for that forwarding overhead;
(d) **drop write verbs on the restricted view** — read-only the
surface so `n`/`e`/`x`/`Ctrl+E`/`Ctrl+N` flash a hint; rejected
because the user explicitly asked for parity, and an
expire-from-this-view flow is the natural way to un-suppress an
alert; (e) **make `RestrictIDs` live** — track the alert's
silenced-by list as it evolves under the silences feed; rejected
because the alert-detail itself freezes `silencedBy` at push time
(the alert poll isn't subscribed by alert-detail), so the
restricted view would have to evaluate matcher semantics
client-side to be honest, and that's a much bigger change than
the bug report justifies.
