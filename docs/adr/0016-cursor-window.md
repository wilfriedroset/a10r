# 0016 — Cursor window encapsulates motion and scroll state

The four list pages (`internal/tui/page/{alerts,silences,groups,
receivers}`) and the tenant page each thread three coupled
integers through every cursor interaction: `Cursor` and `TopRow`
live on `listpage.Base`, `BodyHeight` is written by each page's
`View` and read by its handlers, and the reconcile-on-change
contract lives only in convention. Eight call sites manually
invoke `Base.ReconcileScroll` after a cursor or item-count change;
the `cursor.HalfPageStep` / `cursor.FullPageStep` fallbacks that
handle the pre-`WindowSizeMsg` `BodyHeight==0` case are exposed as
public package functions every handler must remember to call; and
`snapshotFocus()` runs on every `handled==true` return from
`cursor.HandleMotion`, including the `j`-on-last-row case where
the cursor did not actually move. The interface to "move a cursor
in a scrolling table" is "thread three ints through three helpers
in the right order, and never forget the fallback."

This ADR introduces `cursor.Window` — a value type with private
`cursor`, `topRow`, `bodyHeight` fields and methods
`MoveCursor(key, items) (changed, handled bool)`, `SetIndex(i,
items)`, `SetViewport(height, items)`, `Clamp(items)`, `Index()`,
`TopRow()`. Every state-changing method reconciles `topRow`
internally against the current `(cursor, items, bodyHeight)`, so
the eight manual `ReconcileScroll` call sites collapse to zero and
the View→handler implicit-ordering footgun goes away. The
fallback for `bodyHeight==0` lives inside the type;
`cursor.HalfPageStep` / `cursor.FullPageStep` are deleted from the
public API. The motion method returns two distinct signals:
`handled` for the keymap-walk gate (the existing meaning) and
`changed` for the focus-snapshot gate (new — pages branch on
`changed`, eliminating the no-op snapshot on `j`-at-last-row).

`listpage.Base` embeds `cursor.Window` so the four list pages get
`p.Index()` / `p.MoveCursor(...)` / `p.SetViewport(...)` via field
promotion; the `tenant/` page — which does not embed `Base` per
ADR-0013 — holds a `cursor.Window` as a private field. The two
adapters validate the seam: the type is not speculative. Zero
value is usable (cursor=0, topRow=0, bodyHeight=0 with the
fallback applied internally), so tests construct via
`cursor.NewWindow(cursor, topRow, bodyHeight int)` and pages need
no explicit initialisation in their constructors. Fields stay
unexported to keep the invariants type-enforced rather than
convention-enforced.

This tightens part of ADR-0013. That ADR justified `Base`'s flat
exported fields as "simple value state with no invariants —
getters/setters would be boilerplate." The claim does not survive
the field-comment audit: `BodyHeight==0 before first
WindowSizeMsg`, `TopRow reconciled with Cursor every frame`, and
the eight-site reconciliation contract are all invariants the
field comments document but the type does not enforce. ADR-0013's
broader decision — `Base` as an embedded substruct rather than a
`tea.Model` framework — stays; this ADR tightens the cluster of
fields where the invariants are real and load-bearing.

Considered and rejected: (a) a `listpage.Window` type instead of
`cursor.Window` — the tenant page would not share the type since
it does not embed `Base`, collapsing the seam to one adapter and
weakening the deepening; (b) keeping the step helpers public for
callers who want them without a Window — speculative future
caller, and the fallback semantics travel with the Window concept
by design; (c) a callback-style `MoveCursor(key, items, onChange
func()) bool` for the focus snapshot — re-introduces the
`Base.Recompute` callback pattern (panic-at-runtime invariant
check, wired at constructor) that this deepening is otherwise
escaping; (d) `Window` owning an `items func() int` source rather
than taking `items int` per call — couples the type to page-
specific state ownership and forces every page to commit to one
item-count source even when handlers and recompute use different
ones; (e) keeping fields exported for test ergonomics — defeats
the invariant-enforcement that is the whole point, and the test
churn collapses to a one-liner `cursor.NewWindow(...)` replacement
at literal-struct sites.
