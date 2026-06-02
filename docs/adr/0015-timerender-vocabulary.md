# 0015 — Time vocabulary lives in `internal/tui/timerender`

CONTEXT.md names four time-rendering shapes (**relative time**,
**absolute time**, **remaining**, **next attempt**) and warns
explicitly against muddling them, but the implementations had
scattered across three packages by accident of arrival order:
`internal/tui/header/header.go` owns `FormatRelative` /
`FormatAbsolute` / `FormatDuration` because the header bar was
the first caller; `internal/tui/page/alert/alert.go` owns
`formatRemaining` because the alert detail page got the first
"expires in" narrative line; `internal/tui/page/listpage/error_band.go`
owns `nextAttemptLabel` because the error band landed last. A new
caller has no canonical place to look, and `MatchersFromLabels`
escaping `internal/tui/form/silence` (now consumed by alerts,
groups, and the alert detail page) is the precedent we want to
avoid — a domain primitive ends up exported from whichever package
needed it first. This ADR introduces `internal/tui/timerender` as
the home for the four CONTEXT.md vocabularies plus a `Duration`
primitive shared by status-page uptime rendering and `NextAttempt`'s
single-unit ladder. `header` keeps its chrome rendering and imports
`timerender` for the timestamps it shows.

The `TimeFormat` enum (`Relative` / `Absolute`) moves from `app` to
`timerender`. The enum and the function that branches on it want to
live together: today's three duplicated `if p.timeFormat ==
TimeFormatAbsolute { FormatAbsolute(ts) } else { FormatRelative(now,
ts) }` ladders in `alerts/render.go`, `silences/render.go`, and
`alert/alert.go` collapse to `timerender.Display(p.timeFormat,
p.now(), ts)` once the enum is colocated with the helper.
`TimeFormatChangedMsg` stays in `app` because it is a routed
`tea.Msg`, but its `Format` field is now typed `timerender.Format` —
`app` imports the utility, not the other way round.

The interface is plain functions, not a `Renderer` value type with
mutable format state. The format toggle's state is genuinely
page-level (pages already hold a `timeFormat` field for sort
indicators, header rendering, and Update routing), so concentrating
it in a Renderer would shave one field per page without changing
where the state actually lives. Pure functions also match the
existing style and keep the test surface mechanical — table-driven,
no construction ceremony. The lingering duplication is three
one-line `TimeFormatChangedMsg` handlers across the polled list and
detail pages; that is accepted as page-owned UX state rather than
plumbing to centralise.

`Remaining` is contract-tightened: the helper returns the empty
string for non-positive durations (`now >= future`) so the
vocabulary stays strictly forward-looking per CONTEXT.md. The alert
detail page's `"expired"` label moves into the page — it is alert-
domain UX, not a property of remaining time. Without the tightening,
a future caller (silence-detail page, group page) would inherit the
alert-specific past-case string and either render it wrong or
hand-strip it. `Display` and `Absolute` keep their `""`-on-zero
contract; `NextAttempt` keeps its `"retrying now"` past-due contract
because a sub-second deadline means the tick is already late, not
that the poller has nothing to render.
