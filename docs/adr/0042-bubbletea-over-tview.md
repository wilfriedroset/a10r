# 0042 — bubbletea (not tview) is the TUI foundation

a10r is a k9s-shaped Alertmanager TUI: header context strip,
a dense filterable/sortable table body, `:` command and `/`
filter modes, a contextual hint strip, modal confirm/form
flows, and a breadcrumb page stack. Two Go libraries can carry
that shape — [bubbletea](https://github.com/charmbracelet/bubbletea)
(Elm-style Model/Update/View, the Charm ecosystem) and
[tview](https://github.com/rivo/tview) (imperative widgets on
tcell, what k9s itself is built on). a10r builds on bubbletea,
with `bubbles` for ready-made widgets, `lipgloss` for styling
and layout, and `huh` for the silence form. `teatest` plus
`x/exp/golden` carry the rendering tests.

The decision turns on the project framing. a10r is a pet
project with no delivery deadline, so the one axis where tview
dominates — fastest path to k9s parity, because Table, Flex,
Pages, Modal, Form, InputField, and Frame all ship as
primitives you assemble rather than build — is worth less than
it looks. What we trade for it is worth more under the same
framing: Model/Update/View is trivially unit-testable
(`m2, cmd := m.Update(msg)`, no terminal), `teatest` does
golden-file snapshots of a full program (something tview
fundamentally cannot do), and the Charm constellation
(lipgloss, huh, glamour, bubblezone, log) composes cleanly
on top — tview has no comparable siblings. bubbles already
covers ~80% of a k9s-shaped UI (table, list, textinput,
viewport, help, key, spinner, paginator); the gaps — modal
overlay, command palette, page stack, focus delegation, hint
strip — are each small declarative state machines that suit
the M/U/V shape and that we own once and reuse across pages.
Real-time polling is idiomatic: a background goroutine calls
`Program.Send(refreshedMsg{…})` and `Update` dispatches, with
no mutex discipline. tview is mature but near-stagnant (v0.42,
small core, no tests in-repo, no companion libs); "stable" is
not "thriving," and contributor onboarding and testability win
out for a codebase that means to stay clean from day one.

The known costs are accepted: layouts are lipgloss strings
rather than a `Flex`, which gets fiddly for dense resizable
panes; `bubbles/table` is less battle-tested than tview's Table
under high-frequency incremental updates; and the
re-render-the-world model has a cost for continuously-updating
tables (fine at Alertmanager volumes). Revisit only on a
concrete wall — most likely a dense multi-column table needing
high-frequency incremental updates that `bubbles/table` cannot
carry — or if scope shifts so k9s parity becomes a deadline
rather than an aesthetic target.

Considered and rejected: (a) **tview/tcell** — every k9s
look-and-feel detail is a built-in primitive, the shortest
path to a prototype and the natural choice if parity were a
hard constraint; rejected because it has no first-party test
helpers (the library and k9s's UI layer are both untested,
they test the data layer behind interfaces and we would too),
styling is dated next to lipgloss, Table has no built-in sort,
and the ecosystem is just tview plus tcell — under a no-
deadline pet-project framing the testability and ecosystem
loss outweighs the build-it-yourself cost; (b) **a
derailed-style fork of tview/tcell** (what k9s ships) — out
of bounds per the no-forks principle and unjustified for a
project with no parity deadline; (c) **gocui or a lower-level
tcell-direct build** — more primitive than tview with none of
its widget payoff. A wider ecosystem audit (charmbracelet `log`
for logging, `vhs` for demos, `wish` for an SSH server,
`bubblezone` for mouse) informed the choice but is not
load-bearing; only the foundational decision is recorded here.
