# 0030 — Bundled skin set permits in-tree-authored entries

ADR 0024-era work (the k9s-skins-dropin migration) established that
`internal/tui/theme/skins/` is the bundled set, fed by `make
skins-sync` from upstream sources pinned in `SOURCES.yaml`. The
implicit reading was "bundled = synced from upstream"; in-tree
skins authored alongside the synced `catppuccin-*` files do not fit
that reading. This ADR records the explicit policy: the
bundled set may contain in-tree-authored skins, provenance recorded
in `SOURCES.yaml` under a separate top-level `authored:` block. The
`authored:` block is informational — `make skins-sync` ignores it —
and lists the file basenames plus an optional `palette_source`
record (repo + commit + license + source file) so a future reader
can answer "where did the hex anchors come from and against what
upstream version?" without grep-archaeology.

The reasoning is small-surface. The user-facing value of bundling
is `--theme <name>` working on a fresh install; the catppuccin set
proves this is worth shipping. A design system that does not
publish k9s-format skins cannot be mirrored from an upstream — such
a family is either authored in-tree or done without. Authoring
in-tree gives the same fresh-install discoverability as catppuccin
at the cost of a second governance regime; the `authored:` block
makes the regime explicit so the directory listing is not
ambiguous. Drift between an authored skin's palette source and the
recorded `palette_source.commit` is silent today; a `skins doctor` lint
that walks `authored:` and warns on stale pins can land later if
the maintenance load justifies it.

Considered and rejected: (a) **user-side only** (ship authored
skins as files under `examples/` for users to copy into
`<config-dir>/a10r/skins/`) — the skins become second-class
citizens, discoverable only by copying a file, and `--theme
<name>` does not work out of the box; the discoverability
loss is the cost we are paying bundling for and giving it up
defeats the point; (b) **sibling embed directory**
(`internal/tui/theme/skins/` for synced + `skins-authored/` for
authored, both embedded, two `embed.FS` paths in the loader) — two
cascading lookups and twice the embed plumbing for a four-file
delta when a single YAML manifest already carries the origin
metadata cleanly; (c) **implicit convention** ("absence from
`sources:` means authored", no `authored:` block) — a future
contributor adding a synced skin who forgets to update
`SOURCES.yaml` is indistinguishable from one intentionally
authoring in-tree; the directory listing becomes ambiguous and
`git blame` is unhelpful for the question "is this file meant to
be synced?"; (d) **runtime validation** (extend `make skins-sync`
to fail if any `*.yaml` in `skins/` is in neither `sources:` nor
`authored:`) — robust but adds yq logic to detect a class of error
that has not yet occurred; the inventory check is a five-minute
review-time task and the build complexity is not yet earned.
