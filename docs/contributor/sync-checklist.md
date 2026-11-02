# Sync checklist

Some changes land in several files. Find the heading for the kind of
change and touch every place listed under it. This is a reviewer
checklist, not a lint: update it when the shape of the tree moves.

## A new key binding

- the page's `Bindings()` slice, or the global layer in
  `internal/tui/app/app.go`
- the `Dangerous: true` tag in `internal/tui/action/action.go` when the
  key mutates remote state, and the `Guarded: true` tag in the same
  file when a `guardrails:` rule denies the verb on the target tenant
- the per-view table in `docs/end-users/keybindings.md`
- the reserved-key list in `docs/adr/0043-keybinding-contract.md` when
  the key becomes load-bearing
- the user-override reject list in `internal/config/keys.go` when the
  key must not be rebound
- `reserved` in `internal/config/columns.go` when the key is an
  uppercase letter on the alerts or group detail page

## A new guardrail action

- the constant and `knownActions` in `internal/guardrail/guardrail.go`
- the verb list in the Guardrails section of
  `docs/end-users/configuration.md`
- the `Guarded` tag (`internal/tui/action/action.go`) that each page's
  `Bindings()` sets on a binding that runs the verb
- the `gateWrite` call (defined in `cmd/guardrail.go`) in the `cmd/`
  command that runs the verb

## A new config field

- the struct and its validation in `internal/config/types.go`
- `internal/config/testdata/valid_full.yaml`
- the reference table in `docs/end-users/configuration.md`
- the wizard in `cmd/init.go` and `internal/wizard/` when a first-run
  user must set it
- the report in `internal/report/info.go` and its goldens in
  `internal/report/testdata/info_*.golden` when the value is worth
  showing

## A new CLI flag or subcommand

- the cobra wiring under `cmd/`
- `docs/end-users/cli.md`
- the embedded agent skill `internal/skill/SKILL.md`
- `docs/end-users/output-formats.md` when the flag changes the shape of
  the output

A command marked `Hidden: true` is a maintainer tool rather than a
user surface, so it lands in `cmd/` and in `CONTRIBUTING.md` only.
It stays out of the two end-user documents above on purpose. Keep
its `GroupID` all the same, so unhiding it later cannot drop it into
the ungrouped bucket.

## A new exit code

- the constant in `cmd/exit.go`
- the table in `docs/end-users/exit-codes.md`
- the table in `internal/skill/SKILL.md`
- `docs/adr/0009-exit-code-table.md`, which calls the table append-only

## A new page

- the `:` alias registration in `internal/tui/boot/resolver.go`
- the page's `Crumb()`, which `internal/tui/footer/crumbs.go` renders
- the RESOURCE column in `internal/tui/help/help.go`, which derives
  from `Bindings()`
- a section in `docs/end-users/keybindings.md`
- the package layout in `ARCHITECTURE.md`
- the alias collision list in `docs/end-users/configuration.md` when the
  page adds a built-in alias
- `sortResources` in `internal/tui/boot/boot.go` when the page
  remembers a sort column -- list the page's exported `ViewName`, the
  key it passes to `Sorter.Bind`, and build the page in
  `TestSortResources_CoverEveryPageSortKey`

## A new built-in column on the alerts or group detail page

- the page's `columns()` in its `render.go`, and its sort column set
  when the column sorts
- `titles` in `internal/config/columns.go`, and `reserved` when the
  column has a `Shift+<letter>` sort -- each page's
  `TestColumnRules_ConfigMatchesThePage` fails when config misses one
- the page's table in `docs/end-users/keybindings.md` when the column
  sorts
- the user-column placement in `docs/end-users/configuration.md` and
  `docs/adr/0048-user-label-columns.md` when the column lands next to
  the user block

## A new `/` filter mode or sigil

- `internal/tui/filter/searchmode.go` and
  `internal/tui/filter/matcher.go` for a plain mode
- `internal/tui/filterexpr` when the mode joins the boolean grammar,
  plus `Compile` in `internal/tui/filterexpr/compiled.go`, which
  decides whether a buffer is accepted and which language refused
  it, and `internal/tui/page/listpage/filter.go` for the match
  highlight
- the mode table in `docs/end-users/keybindings.md`, which cites the
  mode labels as contract

## A new skin or skin field

- `internal/tui/theme/schema.go` and `internal/tui/theme/styles.go`
- `internal/tui/theme/SOURCES.yaml`, for provenance
- `docs/contributor/skin-authoring.md`
