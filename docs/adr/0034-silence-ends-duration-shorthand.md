# 0034 — Silence form Ends shorthand: AM grammar, stricter ordering, named capital rejection

The silence form's `Ends` field parsed durations with stdlib
`time.ParseDuration`, which accepts only `ns/us/ms/s/m/h` — so `7d`
fell through to the RFC3339 branch and surfaced the misleading error
`parsing time "7d" as "2006-01-02T15:04:05Z07:00": cannot parse "7d"
as "2006"`. Alertmanager's own UI (`ui/app/src/Utils/Date.elm`)
accepts the wider unit set `w/d/h/m/s`, floats, mixed-unit
(`1w2d3h`), spaces between terms, and order-free terms; a10r adopts
the same units and floats and mixed-unit but tightens two AM
behaviours that look like bugs (`2h7d` and `1d1d` both parse under
AM, summing blindly) into named rejections. The parser lives at
`internal/tui/timerender.Parse` so it sits beside the existing
`Duration(d) string` renderer — both directions of the s/m/h/d
vocabulary in one file. The input grammar accepts `w` even though
`timerender.Duration` caps at `d`; the asymmetry is deliberate per
**Duration shorthand** in CONTEXT.md (operators type `1w`, read `7d`).

a10r overloads a single `Ends` field rather than mirroring AM's
three-field shape (RFC3339 `startsAt` + RFC3339 `endsAt` + free-text
`duration`, bidirectionally linked via live cross-field recompute).
The trade is fewer focus stops and no live recompute plumbing in
exchange for losing the live-readout affordance — acceptable because
a TUI form already favours keystroke economy and the operator's
mental model for "how long" is `2h`, not a derived second readout
beneath an RFC3339 timestamp.

Capital `M`/`W`/`Y` are rejected with **tailored** messages naming
the source of confusion: `1M → "M is not a unit; m means minute
(1m=60s); use 30d if you meant ~month"`. The single-letter `m`
collision with cron / human English "month" is the documented
footgun; the error names it instead of relying on the operator to
notice that 1m only delayed expiry by a minute. AM rejects the same
inputs with a flat `Wrong duration format` — a10r's tailored
messages are strictly more helpful at no parser cost.

A faint inline suffix `7d · 1w2d · m=min` is anchored at a FIXED
column (sized to the longest plausible duration shorthand,
`1w2d3h4m` = 8 cols) so it does NOT slide as the operator types —
the original float-past-the-value design was reverted in response
to a reported per-keystroke horizontal jitter where the cue moved
right on every key. The trade is a small detached gap on a short
value in exchange for a column that stays put; for a cue the eye
ignores once read, a still column beats a close one. Once the value
outgrows the shorthand range (an RFC3339 timestamp is being
entered, ~20-25 cols) the suffix elides and the input reclaims the
full width to show the timestamp — correct, not a regression, since
the duration cue is meaningless during timestamp entry. The
placeholder (`2h`) vanishes on first keystroke, but the suffix
survives the whole duration edit so the disambiguation cue stays on
screen at the moment the operator is at risk of typing `1m`
thinking month. The tenant row's `[Enter to change]` hint
(`tenantRow` in `render.go`) is the precedent for inline
affordances + elision; the fixed-column anchor is the deliberate
divergence.

Considered and rejected: (a) **stdlib + Prometheus `model.Duration`
dep** — adds `y` and `ms` we don't render anywhere and silently
accepts the same `2h7d` AM-quirks; (b) **three-field AM shape** —
live bidirectional recompute is heavy bubbletea plumbing for a
read-back the TUI doesn't need; (c) **placeholder rewrite only** —
hint dies on first keystroke, exactly when `1m`-as-month is at
risk; (d) **form-level header line** — broader than needed
(Starts doesn't take shorthand), reads as misleading; (e) **extend
Starts symmetrically** — out of scope for the bug report, no
operator has asked, and the Starts mental model is "when" not
"how long".
