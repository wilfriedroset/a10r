# a10r

Terminal UI for Alertmanager / Mimir: alert triage, silence authoring,
multi-tenant scopes.

## Language

### Time rendering

**Relative time**:
The distance between `now` and a timestamp, rendered as `X ago` when in
the past or `in X` when in the future. Single-unit (s/m/h/d). Used in
compact table columns (alert age, silence STARTS / ENDS).
_Avoid_: age (past-only), remaining (mixed-unit prose), ETA.

**Absolute time**:
The same timestamp rendered as `YYYY-MM-DD HH:MM:SS` in local zone,
toggled app-globally with `t`.

**Remaining**:
The mixed-unit forward-looking duration (`2h13m`, `4d`) used in
narrative fields such as the alert-detail `expires in …` line. Not used
in tables.
_Avoid_: countdown, ETA.

### Silence lifecycle

**Active silence**:
A silence whose window covers `now` (StartsAt ≤ now < EndsAt). ENDS is
in the future → rendered as `in X`.

**Pending silence**:
A silence scheduled for a future window (now < StartsAt). STARTS is in
the future → rendered as `in X`.

**Expired silence**:
A silence whose window has elapsed (EndsAt ≤ now). ENDS is in the past
→ rendered as `X ago`.

## Relationships

- A **silence** is exactly one of **active**, **pending**, **expired**
  at any given moment (backend-decided, client renders what it sees).
- **Relative time** and **absolute time** are two render modes for the
  same underlying timestamp, swapped by the global `t` key.
- **Relative time** (compact, single-unit) and **remaining**
  (mixed-unit, prose) are two distinct rendering shapes — the former
  for table columns, the latter for narrative fields.

## Example dialogue

> **Dev:** "The ENDS column always shows `now` for active silences — can
> we get `in 2h` like expired ones show `2h ago`?"
> **Maintainer:** "Yes — that's relative time. The helper today is
> past-only; we'll extend it to handle future deltas symmetrically, so
> active silences render `in X` and pending silences' STARTS does too."
