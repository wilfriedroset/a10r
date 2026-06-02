## 0031 — Clock injection seams consolidated to `internal/clock`

Three packages each declared their own `Clock` interface and
`SystemClock` production implementation: `internal/tui/keys`,
`internal/tui/poll`, and `internal/doctor`. The keys and doctor
variants were both just `Now() time.Time` plus a stdlib-backed impl;
poll added `After(d)` and `NewTimer(d)` to cover the backoff loop's
need to cancel a pending timer when the user manually refreshes. A
comment audit flagged the rule-of-three: a fourth time-seam consumer
(a future cache TTL, a delayed
flash auto-clear, anything currently using bare `time.Now()` inside a
testable function) would force the project to pick which of the three
to copy from. Lift now so the question never arises.

`internal/clock` exports two interfaces and one concrete impl. `Now`
is the minimal `Now() time.Time` shape every consumer needs — doctor
TTL checks, keys chord deadlines, and anything else that only cares
about "what time is it?". `Clock` is the richer shape (Now + After +
NewTimer) the poller uses to schedule and cancel ticks. The
production `System` value satisfies both — `*time.Timer`-backed
`Timer` wrappers (`systemTimer`) live in the same file because the
interface and its only sanctioned impl belong together. Tests keep
their own `fakeClock` types: the seam is the interface, not the
production value, so per-package fakes that need different fast-
forward semantics (deterministic chord timing, fan-out backoff
sequencing) stay where they're used rather than getting flattened
into a one-size-fits-all test helper.

Considered and rejected: (a) leave the rule-of-three intact and
revisit on the fourth caller — the auditor's recommendation. Cost: the
fourth caller pays the migration tax instead of the third; not
materially different in effort, just shifted. Lifting now while every
consumer is already being touched for the wider comment-audit sweep
makes the change atomic. (b) Consolidate only the `Now()`-only
variant and leave the poller alone — would mean two parallel "clock"
packages (`internal/clock` for the minimal seam, `internal/tui/poll`
keeping its own richer one); each new contributor has to learn which
to import. Single package with two interfaces communicates "richer
extends minimal" without duplication. (c) Make `Clock` embed `Now`
explicitly — works in Go but adds a layer the call sites do not need;
the `System` value satisfying both interfaces via method-set membership
already gives the upgrade path without forcing an embed relationship.
