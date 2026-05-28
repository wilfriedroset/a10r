# 0038 — Hint grid grows a curated global prelude, help overlay grows a COMMANDS column

The **hint grid** in the top panel was strictly "page bindings" — a
verbatim render of the active page's `Bindings()`. The **help
overlay** had four columns (RESOURCE / GENERAL / NAVIGATION /
HOTKEYS) listing the dispatcher's catalogue. Neither surface taught
a first-time operator the `:`-bar exists or what it accepts. The
`?` global was registered but invisible until the user knew to
press it; the `:`-bar's alias catalogue lived only in the source
tree (`boot/resolver.go`) and the doc website. Both gaps are
discoverability holes: power users learn around them, newcomers
bounce off.

This ADR records three structural shifts:

1. **The hint grid prepends a curated global prelude.** Today the
   prelude has exactly one entry: `?` (description `help`). The
   panel widens its `state.Hints` contract from "page bindings" to
   "curated global prelude + page bindings", with dedup against a
   page that misroutes the global. A first-time operator sees the
   discovery affordance painted on every page without having to
   know it exists. Future global verbs deemed load-bearing for
   discoverability join the prelude; the rest stay in the help
   overlay only.

2. **The help overlay grows a COMMANDS column.** Five columns
   total: RESOURCE | GENERAL | NAVIGATION | HOTKEYS | **COMMANDS**.
   The new column lists the resolver's built-in alias catalogue,
   folded by synonym so `silences` and `sil` share one row
   (`silences, sil`). A `USER` sub-heading appears underneath when
   the operator has registered any aliases, with each row formatted
   as `short → expanded` so the binding self-documents. The
   sub-heading is intentionally rendered weaker (unbold, same
   colour as the column heading) so a reader registers it as a
   nested section, not a sixth peer column.

3. **The `:` chip relabels to `:cmd`.** The dispatcher still
   matches on a bare colon, but the help overlay and the hint
   strip render `<:cmd>  Command mode` so the operator reads
   "type colon, then a command name" instead of guessing what
   `<:>  command` means. `action.Action.DisplayKey` is the new
   override mechanism: a single `SetActionDisplayKey` call on the
   dispatcher flips every chip renderer at once.

The supporting plumbing:

- `cmdbar.Resolver` gains `RegisterGroup(names, h)` so synonyms are
  an explicit declaration instead of inferred from Go function
  equality. `Groups()` returns the resulting catalogue sorted by
  canonical name; `UserAliases()` returns the user-registered
  `short → expanded` pairs (with leading/trailing whitespace
  trimmed at registration so the rendered arrow never paints stray
  padding).
- `boot/resolver.go` switches the four synonym pairs
  (silences/sil, receivers/rec, groups/gr, tenant/tenants) from
  twin `Register` calls to a single `RegisterGroup` call each;
  singletons (alerts, status, q) stay on `Register`.
- `keys.Dispatcher` carries `DisplayKey` through `Bindings()` and
  exposes `SetActionDisplayKey(name, displayKey)` for the override.
  Re-registering via `SetAction` clears any prior override (last-
  write-wins across every field), so a caller wanting a persistent
  override must re-apply it after the second `SetAction`.

The hint grid's logo-drop / col-shrink / trailing-drop reflow
(ADR 0036) continues to apply unchanged — the prelude is one extra
cell, well within the cap. The help overlay's existing 12-cell
minimum column width (`max(width/len(cols), 12)`) absorbs the
fifth column without further work.

Considered and rejected: (a) **hint-grid prelude on the caller
side** — keeps the panel mechanical but spreads the chrome
contract across two packages; panel-side ownership lets a future
call site of `RenderTop` inherit the prelude for free; (b) **show
every LayerGlobal binding in the hint grid** (`?` `:` `/` `t`
`Esc` `q` `Ctrl+C` `Ctrl+T` plus digits) — consistent but loud,
and the digits collide with the tenant column. The hint grid is
the front porch — discovery, not catalogue; (c) **`<:cmd>` as a
separate GENERAL row alongside `<:>`** — two rows for one binding,
redundant; (d) **inline user aliases into the COMMANDS rows** —
collapsing them onto the built-in's row would mislead the help
reader, since a user `:prod` registered as `tenant prod` would
appear as a synonym of `:tenant` but `:prod alerts` works and
`:tenant alerts` does not; (e) **hide user aliases entirely** —
removes the only in-TUI discoverability path for the operator's
own shorthands; (f) **a 6th column for user aliases** — wastes
horizontal budget on a list that is often empty; the in-column
sub-section degrades to nothing gracefully when there are none.
