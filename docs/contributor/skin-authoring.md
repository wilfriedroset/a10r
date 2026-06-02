---
title: Authoring a bundled skin
status: draft
audience: a10r contributors adding a skin to internal/tui/theme/skins/
---

# Authoring a bundled skin

This doc is for contributors adding a **bundled skin** — a file
under `internal/tui/theme/skins/` shipped in the binary. The skin
schema is a drop-in of k9s's, so the upstream
[k9s skin reference](https://k9scli.io/topics/skins/) documents
every field; a user-side skin under `<config-dir>/a10r/skins/`
uses the same schema. For the policy that permits
in-tree-authored skins alongside upstream-synced ones, see
[ADR 0030](../adr/0030-in-tree-bundled-skins.md).

---

## The two governance regimes

`internal/tui/theme/skins/` contains skins from two regimes,
declared in `internal/tui/theme/SOURCES.yaml`:

| Regime | Manifest block | How it changes |
|---|---|---|
| **Synced** | `sources:` | Mirrored verbatim from an upstream repo at a pinned commit. Refreshed by `make skins-sync`. Hand-editing the file in-tree drifts from upstream and gets clobbered on next sync |
| **Authored** | `authored:` | Hand-written and reviewed in-tree. `make skins-sync` ignores them. Edits land via ordinary PRs |

Before adding a skin, decide which regime applies:

- If an upstream maintains a k9s-format skin pack you want to ship,
  add it to `sources:` and run `make skins-sync`. Done.
- If you are authoring a skin from a non-k9s source (a CSS
  framework's palette, a brand design system, a hand-picked
  collection), it belongs in `authored:`.

---

## Authored skin: file checklist

1. Pick a name. Bundled-skin names use `<family>-<mode>` with an
   optional `-transparent` suffix (e.g., `acme-dark`,
   `acme-dark-transparent`). The name must match the file
   basename (`<name>.yaml`) and the loader's allowed alphabet
   (`^[a-zA-Z0-9_.-]+$`).
2. Write the file under `internal/tui/theme/skins/<name>.yaml`.
3. Add an entry to `SOURCES.yaml` under `authored:` (see below).
4. Extend the test inventory in
   `internal/tui/theme/loader_test.go` (`bundledNames` slice, plus
   the transparent-variants list if it is a transparent skin).
5. Run `go test ./internal/tui/theme/...` — every bundled skin
   must compile cleanly.
6. Update `README.md` and `examples/demo.yaml`'s `theme:` comment
   if the new skin is part of a new family worth surfacing.

---

## File header — disclaimer

When the skin is derived from a third-party design system, prepend
a comment that records the inspiration source and explicitly
disclaims affiliation. This is courtesy, not a legal requirement,
but the project default is to add it. Example:

```yaml
# acme-dark — a10r skin inspired by the Acme Design System
# (https://example.com/acme/design-system, Apache-2.0).
#
# a10r is an independent open-source project and is not affiliated
# with, endorsed by, or supported by Acme. The "Acme" name is used
# nominatively to identify the visual inspiration.
```

---

## Anchor values: `body.fgColor` / `body.bgColor`

These two fields are mandatory; everything else cascades from them
(the role map follows the k9s schema — see the
[k9s skin reference](https://k9scli.io/topics/skins/)).
A worked example for a hypothetical `acme-*` family:

| Skin | `body.bgColor` | `body.fgColor` |
|---|---|---|
| `acme-dark` | `#1a1a1a` (neutral-900) | `#f2f2f2` (neutral-050) |
| `acme-light` | `#ffffff` (white) | `#4d5592` (the brand text token) |
| `acme-dark-transparent` | `default` (terminal-native) | `#f2f2f2` |
| `acme-light-transparent` | `default` | `#4d5592` |

The rule of thumb when picking anchors for a new authored family:
do not reuse a brand accent color for `body.bgColor`. Chrome
accents (header logo, focus border) only read against the body
background if they are not the *same* color. Use the design
system's canonical surface for `body.bgColor` (white for light;
neutral-900 for dark) and reserve brand colors for chrome.

---

## Shade rule

Brand semantic scales (`primary`, `information`, `success`,
`warning`, `critical`, `neutral`) run 000 → 900 (lightest →
darkest). The same role needs different shades on dark vs light
backgrounds to stay legible. The default rule:

- **Dark-mode skin**: `<scale>-300`
- **Light-mode skin**: `<scale>-500`

This applies uniformly to severity, silence-state, flash, and any
other slot that maps to a semantic scale.

### Escape hatch

When `-300`/`-500` looks visibly wrong for a specific role (the
scale is not perceptually uniform, or the role's role demands
extra punch), the author may deviate. Deviations must be
documented in a `# Tuning notes` block at the top of the file,
below the disclaimer, listing every role that diverges from the
rule and why. Example:

```yaml
# Tuning notes:
# - severity.warning uses warning-600 instead of warning-500 in
#   light mode: warning-500 (#ff8b00) reads as low-contrast on
#   white at terminal font sizes; warning-600 (#cc7000) preserves
#   the "amber" reading.
```

A reviewer should be able to grep `Tuning notes` and find every
deviation.

---

## Brand-color anchors (chrome surfaces)

Chrome surfaces (header logo, focus borders, breadcrumbs, table
sorter indicator, cursor) are the closest thing a TUI has to a
marketing surface. They are the right place to use a design
system's brand colors rather than its semantic scales. For
`acme-*`:

| Role | Dark anchor | Light anchor |
|---|---|---|
| `body.logoColor` (header accent) | `#73e3ff` (brand accent) | `#000e9c` (brand primary) |
| `frame.border.focusColor` | `#73e3ff` | `#000e9c` |
| `frame.crumbs.bgColor` | `#73e3ff` (brand accent) | `#4d5592` (brand secondary) |
| `frame.crumbs.fgColor` | `#1a1a1a` | `#ffffff` |
| `frame.crumbs.activeColor` | `#ffd124` (brand highlight) | `#00185e` (brand deep) |
| `views.table.header.sorterColor` | `#ed733d` (brand orange) | `#ed733d` |
| `views.table.cursorBgColor` | `#ffd124` (brand highlight) | `#00185e` (brand deep) |
| `views.table.cursorFgColor` | `#1a1a1a` | `#ffffff` |

The cursor pair must survive both the opaque body bg *and* an
arbitrary terminal bg (the `-transparent` sibling inherits the
cursor hex unchanged). Pick saturated brand colors with high
contrast against both extremes.

**Breadcrumb pills share one fg.** The renderer
(`internal/tui/footer/crumbs.go`) paints the inactive crumb as
`crumbs.fg` text on `crumbs.bg`, and the active crumb as
`crumbs.fg` text on `crumbs.active`. One foreground colour has to
contrast against *both* backgrounds, so pick `fg` against the
*both* pill bgs, not just one. The mistake to avoid: picking a
midtone `fg` that contrasts against `bg` (the ribbon) and only
weakly against `active` (the highlight) — the active pill ends
up illegible. The `acme-*` family also reuses
`crumbs.activeColor = views.table.cursorBgColor` so "current
focus" is the same brand colour on both the breadcrumb pill and
the selected table row.

---

## Transparent variant: derivation rule

A transparent skin derives mechanically from its opaque sibling.
Take the opaque file and:

1. Replace every flat-surface `bgColor:` hex with `bgColor: default`.
   Includes: `body`, `prompt`, `help`, `frame.title`, `frame.crumbs`,
   `views.table`, `views.table.header`, `views.xray`, `views.charts.*`,
   `views.logs`, `views.logs.indicator`, `dialog`, `dialog.buttonBgColor`.
2. **Keep** these bg fields opaque (preserve the hex from the
   opaque sibling):
   - `views.table.cursorBgColor` — selection cursor must remain
     visible against the terminal bg
   - `views.xray.cursorColor` — same reasoning
   - `dialog.buttonFocusBgColor` — focused dialog button must
     remain visually distinct from siblings against terminal bg
     (catppuccin upstream follows the same convention)
3. **Leave every `fgColor` unchanged.** Foreground colors carry the
   skin's identity; transparent variants are *not* a separate skin
   family — they are the same skin shown on the terminal's bg.

The disclaimer header is shared; restate it at the top of the
transparent file (each file is read standalone).

---

## SOURCES.yaml entry

For each authored skin family, add an entry under `authored:`:

```yaml
authored:
  - palette_source:
      repo: https://example.com/acme/design-system
      commit: <pinned-SHA-at-authoring-time>
      license: Apache-2.0
      file: path/to/palette/source
    files:
      - acme-dark.yaml
      - acme-dark-transparent.yaml
      - acme-light.yaml
      - acme-light-transparent.yaml
```

The `palette_source` block is provenance metadata —
`make skins-sync` ignores it. The pinned `commit` lets a future
reader recreate the palette decisions and detect upstream drift.
For families with no upstream palette to pin (a hand-picked
collection), omit `palette_source` and keep only `files:`.

---

## Testing

Extend `internal/tui/theme/loader_test.go`:

- Add every new skin name to `bundledNames`.
- If transparent variants are included, add them to the
  transparent list in `TestLoad_TransparentVariantsKeepBodyBgUnset`.

Do *not* add tests that pin specific hex values per role — the
shade rule and the file are the source of truth for hex choices;
a test pinning them creates a third source and forces a
three-way edit on every tuning. Author discipline lives in this
doc + review, not in the test suite.

---

## Review checklist

For a reviewer accepting an authored skin PR:

- [ ] File header carries the disclaimer where the skin derives
      from a third-party design system
- [ ] `body.fgColor` / `body.bgColor` are present and not the same
      color
- [ ] Shade rule (`-300` dark / `-500` light) holds, or
      deviations are recorded in `# Tuning notes`
- [ ] Transparent variant follows the derivation rule (cursor bg
      kept; every other flat-surface bg → `default`)
- [ ] `SOURCES.yaml` updated with an `authored:` entry
- [ ] `bundledNames` in `loader_test.go` extended
- [ ] `make test` passes; the loader compiles the new skin without
      error
- [ ] README's skin-family bullet updated if this is a new family
