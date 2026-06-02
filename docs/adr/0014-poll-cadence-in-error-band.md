# 0014 — poll cadence rendered in the error band, not via Status query

`internal/tui/poll/poll.go` previously emitted `BackendStatusMsg`
only on `header.ConnState` transitions, so a tenant in sustained
backoff produced one message per outage and pages mirrored only its
`Detail` string into `Base.LastErrors map[string]string`. The
transition-only contract starved the **error band** (CONTEXT.md)
of liveness — operators saw a static "Unreachable" line for the
duration of an outage with no signal that the poller was still
trying. This ADR widens `BackendStatusMsg` with `Failures int` and
`NextAt time.Time`, emits it every tick during failure (recovery
stays transition-only), and replaces `Base.LastErrors` with
`Base.BackendHealth map[string]listpage.BackendHealth` populated
through a new `Base.HandleBackendStatusMsg` helper that the four
list pages call in place of their previously duplicated three-line
handler. The renderer in `error_band.go` appends
`— retrying in <relative>` (or `retrying now` when `NextAt` is
past-due) to the existing single- and multi-offender layouts; the
countdown ticks live because the View already re-renders on
spinner / keystroke / message ticks and computes from `next - now`
at View time — the same shape the success-path footer countdown
already uses.

The asymmetric emission (per-tick during failure, transition-only
on recovery) is deliberate: success-path UI is already driven by
`DataMsg` (which carries its own `NextAt` for the footer's
`next refresh Ns`), so a per-tick BackendStatusMsg on the healthy
path would only re-affirm what `DataMsg` already says. The original
doc claim at `poll.go:69-72` ("avoids re-rendering on every
successful tick") was always about success noise — failure
re-renders are signal, and each emission lets the operator
distinguish a first failure (`1s` out) from a cap-bound sustained
outage (`6s` repeating). `Failures` flows in the wire format and
into `BackendHealth` for future consumers (header tooltip,
`doctor`) but is intentionally **not** rendered in the band today:
the live-ticking countdown already encodes outage age via the
cap-growth schedule, and a `(N tries)` suffix reads weirdly on the
first failure (`(1 try)`) for no incremental signal. The
domain/wire split — `listpage.BackendHealth` is the value the page
holds, `poll.BackendStatusMsg` is the message it consumes,
translated at the `Base.HandleBackendStatusMsg` seam — keeps future
poller-schema changes from rippling through every page's mirror,
and the handler living on `Base` collapses byte-identical
three-line `case poll.BackendStatusMsg:` blocks across
`alerts/handlers.go`, `silences/handlers.go`, `groups/handlers.go`,
and `receivers/receivers.go` into a single call.

Considered and rejected: (a) a thread-safe `Poller.Status(tenant)`
accessor — pages don't hold a `*Poller` reference today, so the
accessor would require either a `PollerRegistry` injected into
every list page (a new dependency edge crossing the listpage seam)
or a wrapper through the App, and its only honest advantage over
per-tick emission was view-time freshness during mid-backoff page
mounts, invisible at the second-precision the band renders; (b)
absorbing the failure cadence into the success-path footer
(`next refresh Ns`) — collapses to one cadence surface but breaks
the footer's load-bearing "am I looking at fresh data?" semantics,
since a backoff retry's `NextAt` would silently hijack the number
operators glance at to gauge staleness; (c) rendering a `(N tries)`
counter in the band — the countdown already encodes outage age via
cap growth, and rendering `(1 try)` on the first failure adds
visual weight for zero incremental signal.
