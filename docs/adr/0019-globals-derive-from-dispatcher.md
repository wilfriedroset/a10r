# 0019 — global keybinding help derives from the dispatcher; `action.Registry` is removed

Adding a global keybinding today touches two surfaces: the
dispatcher call (`SetAction(layer, name, key, handler)` in
`registerGlobalBindings`) and the hand-curated `globalsCatalog()`
slice in the same file that the help overlay's GENERAL column
renders. The two share no programmatic link — the second is just a
slice of `{Key, Description}` literals — so a new dispatcher entry
without a matching catalog entry is silently absent from `?`, and
a renamed binding leaves the catalog stale. Both drifts have
landed in PRs before. A third surface, `action.Registry`, was
designed as the canonical store but never adopted in production:
the struct is constructed at App startup and held on `App.registry`
and `app.Options.Registry`, but production code never calls
`Register` on it — pages return static `[]action.Action` slices
from `Bindings()` directly, and globals never participate at all.

This ADR records the decision to **derive the GENERAL column from
the dispatcher** and **delete the vestigial `Registry`**. The
dispatcher's `SetAction` grows a fifth parameter, `description
string`, that lands on the existing `actionEntry` alongside the
key and handler. A parallel `actionOrder []string` slice on the
dispatcher records names in registration order so `Bindings(layer)
[]action.Action` returns a stable, layer-filtered list — Go maps
have no defined iteration order, and the help-overlay ordering is
load-bearing (`:` and `/` before the escape-hatch `Ctrl+C` per
muscle memory). `globalsCatalog()` becomes a thin wrapper:
`append(d.Bindings(LayerGlobal), action.Action{Key: "r",
Description: "refresh"})`. The lone curated extra is the
*documented-as-global, implemented-per-page* affordance — `r`
(refresh) and `w` (toggle watch) appear in every page's
`Bindings()` and are handled in each page's `Update`, so they are
not LayerGlobal entries today. Keeping the `r` row explicit, with
no programmatic link to the dispatcher, leaves a visible marker
for the next deepening rather than burying it.

`action.Registry`, `App.registry`, and the `Registry` field on
`app.Options` are removed. The ~14 `Registry: action.New()` lines
in the App test fixtures go with them. The `action` package keeps
`action.Action` (the canonical data type that every page's
`Bindings()` returns) and `action.FilterDangerous` (the read-only
filter every page applies to its own slice), which are the
load-bearing exports. `tableMotionsCatalog` stays curated: the
table widget is not the dispatcher, and its motions belong to a
different deepening axis.

Re-ordering `registerGlobalBindings` to match the curated display
order is a strict reorganisation — the function is a flat list of
`SetAction` calls and the new sequence is annotated as
"registered in help-overlay GENERAL-column display order" so a
future contributor adding a binding knows where to insert it.

Considered and rejected: (a) keeping `Registry` and joining the
dispatcher's `Bindings(layer)` to `Registry.Hints("")` by stable
name — predicated on `Registry` being canonical, which production
disproves; adds a join layer over two sources of truth where there
is functionally one; (b) adding `Name` to `action.Action` and
passing the struct to `SetAction` — pollutes ~30 page-`Bindings()`
instances with empty `Name` fields and trades one positional
parameter for three call-site lines per binding; (c) deriving
description from the kebab-case name via
`strings.ReplaceAll(name, "-", " ")` — fragile against any future
name that does not transform cleanly to a label, and the
dispatcher already carries the name so adding one more positional
string is the explicit-and-cheap path; (d) migrating `r`
(refresh) and `w` (toggle watch) into the dispatcher as real
LayerGlobal entries that emit `RefreshRequestedMsg` /
`WatchToggleMsg` — load-bearing and worth doing, but a behavioural
change to every page's `Update`; bundling it with the structural
derivation muddies bisect and review; left as a named follow-up
that the curated `r` row in `globalsCatalog` now points at; (e)
exposing `Bindings()` on the table widget so
`tableMotionsCatalog` also derives — yak-shaving; the widget's
shape is unrelated to globals drift and unblocks no concrete work;
(f) sorting the derived list alphabetically by key — produces
`Ctrl+C` before `q` and `Ctrl+T` before `t`, both reversals of the
keybindings.md ordering principle and the muscle memory the
curated order encodes.
