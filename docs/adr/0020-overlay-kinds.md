# 0020 — overlay surfaces split: async-result `modal.Modal` vs. viewer `help.Help`

`modal.Modal` is the interface every input-capturing overlay
implements: `Init`, `Update`, `View`, `Title`, plus a result type
that satisfies `modal.ResultMsg`. The App shell routes input to
whichever `modal.Modal` is open before the dispatcher fires, then
auto-closes on any `ResultMsg`. The async-result impls
(`confirm.Confirm` and `modal.Picker`) fit cleanly: each carries
a `Cancelled` / `Submitted` / `Yes/No` payload that the caller
acts on. `help.Help` is the outlier: the only impl whose
`ResultMsg` payload is the empty `HelpClosedMsg{}` marker — there
is no decision in flight, only a viewer dismissing. The
cross-package-coupling cost surfaced as a comment on
`modal.HelpClosedMsg`: *"Lives in this package (rather than
internal/tui/help) so that other modals don't need to reach across
packages to satisfy ResultMsg."* The marker rented `modal/`'s slot
to make a type-system convenience work.

This ADR records the decision to **split the routing slot**:
async-result overlays continue to satisfy `modal.Modal`; viewer
overlays own a parallel slot on `app.Model`. The `App` gains a
`help *help.Help` field beside `modal modal.Modal`, with input
routed `if a.modal != nil → modal.Update` *else if* `a.help != nil
→ help.Update` *else* the dispatcher. Render mirrors the same
precedence. The dispatcher is bypassed whenever either slot is
filled, so `?` registered at `LayerGlobal` does not fire while a
modal is open — that preserves today's "decisions are sticky"
invariant, where the user pressing `?` over a pending confirm
would otherwise dismiss the decision off-screen. The reverse case
(opening a modal while help is open) is structurally impossible
because the keys that open modals (`Ctrl+T`, `Ctrl+X`, etc.) are
themselves dispatcher-gated, and the dispatcher is bypassed while
`help != nil`.

`help.ClosedMsg` moves into the `help` package and stops
implementing `modal.ResultMsg`. The App's lifecycle gains a
parallel close branch (`if _, ok := msg.(help.ClosedMsg); ok {
a.help = nil }`) that does not go through the existing
`isModalResult` fan-out. `OpenHelp(opts help.Options) tea.Cmd` is
factory-less because `Help.Init` returns nil — the factory pattern
on `OpenModal` exists so `Modal.Init`'s cmds reach the program
loop, and Help has no such cmds to defer. The call site at
`registerGlobalBindings`'s `?` binding becomes a direct `OpenHelp(
help.Options{...})` instead of the nested `OpenModal(func()
modal.Modal { return help.New(...) })`.

`modal.Modal` shrinks back to "async result-returning overlay" —
matching its package doc, which names the tenant picker and the
confirm dialog. Future overlay decisions have a categorising
rule: a surface that returns a typed payload satisfies
`modal.Modal`; a surface that only shows information for as long
as the user looks at it owns its own slot like `help.Help`.
(Historical note: a third impl, `silencepicker.SilencePicker`,
existed at the time this ADR was written; it was retired in ADR
0035 once the alert-detail `S` binding moved to pushing the
silences list page directly.)

Considered and rejected: (a) collapse the help slot into `app.Model`
state with a `ToggleHelpMsg` and an `App.HandleKey`-style scroll
router — the doc's original shape, but it deletes the routing
slot at the cost of pulling Help's scroll-vs-dismiss key parsing
into the app shell; trades one interface lie for one fewer slot,
and the lie is the load-bearing smell; (b) keep Help as a
`modal.Modal` impl and rename the interface to `Overlay` — papers
over the asymmetry rather than naming it; the async-result impls
genuinely *are* async-result surfaces and want their `ResultMsg`
machinery, `help` genuinely is not; (c) mutually
exclusive at open-time (opening one closes the other) — lets
`Ctrl+X` over open help silently nuke the overlay, a quiet UX
regression; (d) `Help > Modal` precedence (help can open over a
pending confirm) — focus-stealing complexity for no win; the
operator pressing `?` after committing to a destructive flow is
better served by the confirm staying foregrounded; (e) factory-
based `OpenHelp(factory func() *help.Help)` parallel to
`OpenModal` — symmetry without payoff, since `Help.Init` is nil
and the indirection adds three lines per call site for zero
flexibility; (f) treat the package extraction (Help already lives
under `internal/tui/help/`) as resolution-enough — the deletion
test was wrong: pulling Help out of `modal/` does not collapse the
interface, because three real async-result impls remain, but the
conformance smell (Help renting the slot to ship a no-payload
marker) was always the load-bearing half of the deepening and is
unaddressed by the package extraction alone.
