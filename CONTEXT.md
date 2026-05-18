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
in tables. The vocabulary is strictly forward-looking — a non-positive
duration is out-of-domain for Remaining, so the renderer returns the
empty string and the caller owns any past-case label (the alert detail
page renders its own `expired`).
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

### Backend health

**Backend health**:
The per-tenant transport state a list page holds for rendering the
**error band**; carries (state, detail, failures, **next attempt**).
State is one of *connected* / *degraded* / *unreachable*. An entry
exists only while a tenant is not connected; cleared on recovery.
_Avoid_: backend status (the wire-format message that mutates this
value), connection state (header chrome only).

**Next attempt**:
The failure-mode tick clock rendered in the **error band** using
single-unit **relative time** — `retrying in 5s`, `retrying in 1m`.
When the clock is past-due (a tick is in flight), the band suffix
becomes `retrying now`.
_Avoid_: retry deadline, backoff (poller implementation detail).

**Error band**:
The one-line surface above the table that narrates per-tenant
**backend health** for tenants in scope. Empty when every in-scope
tenant is **connected**. Multi-offender layouts collapse to a count
plus the alphabetically first offender's detail and **next attempt**.
_Avoid_: status line, error banner.

## Relationships

- A **silence** is exactly one of **active**, **pending**, **expired**
  at any given moment (backend-decided, client renders what it sees).
- **Relative time** and **absolute time** are two render modes for the
  same underlying timestamp, swapped by the global `t` key.
- **Relative time** (compact, single-unit) and **remaining**
  (mixed-unit, prose) are two distinct rendering shapes — the former
  for table columns, the latter for narrative fields.
- **Backend health** entries exist per tenant only while not
  **connected**; the **error band** renders only in-scope entries.
- **Next attempt** reuses the single-unit **relative time** vocabulary
  with `retrying in` as the prefix instead of bare `in`.

## Example dialogue

> **Dev:** "The ENDS column always shows `now` for active silences — can
> we get `in 2h` like expired ones show `2h ago`?"
> **Maintainer:** "Yes — that's relative time. The helper today is
> past-only; we'll extend it to handle future deltas symmetrically, so
> active silences render `in X` and pending silences' STARTS does too."
