---
title: k9s skins drop-in
status: draft
audience: a10r maintainer, theme contributors
supersedes: docs/design/theming.md
---

# k9s skins drop-in

Replace a10r's bespoke palette+roles skin schema with k9s's native skin schema, so any community k9s skin can be dropped into `<config-dir>/skins/` and used as-is. This doc captures the decisions taken in the grill-me round on 2026-05-06; the implementation diff follows from it.

---

## Goal

Drop any k9s skin file (e.g. one of the 35 in `derailed/k9s/skins/` or any community skin) into `<config-dir>/a10r/skins/` and have a10r render with it. No conversion, no a10r-specific extensions in the file. The bundled set ships in the same format.

---

## Decisions

### D1 — Replace, don't dual-format

a10r's two-layer `palette` + `roles` schema is removed. The Go schema, loader, embedded skins, and tests are scrapped and rewritten to consume the k9s schema directly. Reasoning: pet-project posture, no fork/dual-schema appetite, simpler to maintain a single code path that mirrors a known upstream.

### D2 — Severity, silence and flash colors derive from `frame.status`

k9s skins have no `severity` / `silence_state` / `flash` block. We derive these from `k9s.frame.status`, whose semantics (event-stream colors) line up with our domain:

| a10r role               | k9s field                       |
|-------------------------|---------------------------------|
| `severity.critical`     | `frame.status.errorColor`       |
| `severity.warning`      | `frame.status.highlightColor`   |
| `severity.info`         | `frame.status.newColor`         |
| `severity.unknown`      | `frame.status.killColor`        |
| `silence_state.active`  | `frame.status.addColor`         |
| `silence_state.pending` | `frame.status.highlightColor`   |
| `silence_state.expired` | `frame.status.killColor`        |
| `flash.success`         | `frame.status.addColor`         |
| `flash.info`            | `frame.status.newColor`         |
| `flash.warn`            | `frame.status.highlightColor`   |
| `flash.error`           | `frame.status.errorColor`       |

No `a10r:` extension block. Drop-in stays pure.

### D3 — Two-tier missing-field policy with k9s-stock fallback

Mandatory at load time:
- `body.fgColor`, `body.bgColor`

Soft-mandatory (cascade `skin → k9s stock → fail`):
- `frame.status.{newColor, errorColor, addColor, killColor, highlightColor}`

The k9s stock palette (taken verbatim from `derailed/k9s/internal/config/templates/stock-skin.yaml`):

```
newColor: lightskyblue   modifyColor: greenyellow   addColor: dodgerblue
errorColor: orangered    highlightColor: aqua       killColor: mediumpurple
completedColor: lightslategray
```

Spot-check across all 35 upstream skins: 33 have all five status fields explicitly. The two outliers (`transparent.yaml`, `vercel.yaml`) rely on k9s stock defaults; with the cascade they load cleanly.

Everything else cascades to a documented fallback chain (see "Role mapping" below). Only the two-tier set surfaces a load-time error.

### D4 — Color value grammar

Three accepted forms per skin file:

1. `#rrggbb` — 6-digit hex
2. `default` — terminal-native (lipgloss: omit `.Foreground()` / `.Background()` call)
3. SVG/CSS named colors — `dodgerblue`, `aqua`, `mediumpurple`, etc.

Numeric ANSI palette values (`color9`, `0`–`255`) are not accepted; no public k9s skin uses them.

Parser shape:

```
parseColor(s) → color.Color | error
  s == ""        → error "missing"
  s == "default" → terminal-default sentinel
  /#[0-9a-f]{6}/ → hex
  s ∈ svgNames   → that color
  else           → error "unknown color %q"
```

The SVG name table is vendored from `tcell/v2/color.go` (~140 entries). Closed set; not auto-synced.

### D5 — Bundled skin set

Eight files, copied from `catppuccin/k9s` upstream (`dist/`):

```
catppuccin-frappe.yaml          catppuccin-frappe-transparent.yaml
catppuccin-latte.yaml           catppuccin-latte-transparent.yaml
catppuccin-macchiato.yaml       catppuccin-macchiato-transparent.yaml
catppuccin-mocha.yaml           catppuccin-mocha-transparent.yaml
```

Default skin: `catppuccin-mocha` (unchanged). The `-transparent` variants are recommended for users with curated terminal backgrounds (per the "TUI chrome on terminal default bg" preference) but not the default, since "works regardless of terminal config" wins for first-impression UX.

### D6 — Sync via Make target with pinned commit

`internal/tui/theme/skins/SOURCES.yaml` records origin + commit + license + file list:

```yaml
- repo: https://github.com/catppuccin/k9s
  commit: <40-char SHA>
  license: MIT
  files:
    - dist/catppuccin-frappe.yaml
    - dist/catppuccin-frappe-transparent.yaml
    - dist/catppuccin-latte.yaml
    - dist/catppuccin-latte-transparent.yaml
    - dist/catppuccin-macchiato.yaml
    - dist/catppuccin-macchiato-transparent.yaml
    - dist/catppuccin-mocha.yaml
    - dist/catppuccin-mocha-transparent.yaml
```

`make skins-sync` clones the pinned commit, copies the listed files into `internal/tui/theme/skins/`, and fails if the working tree shows untracked diffs (forces a conscious `git add`). No `go:generate`, no network during build.

### D7 — Permissive YAML decoding

`KnownFields(false)`. We model only the subset of the k9s schema we read; upstream additions (`info.cpuColor`, `views.charts.defaultDialColors`, etc.) are silently ignored. A typo in a field name silently falls through the cascade — visual bug, no log entry. A `a10r skins doctor <file>` lint command can be added later if anyone wants strict-decode + unknown-field reporting; not in scope here.

### D8 — `UserDir` wiring

`cmd/tui.go:345` currently calls `(&theme.Loader{}).Load(name)` with empty `UserDir`, silently disabling user skins. As part of this work, resolve `<config-dir>/a10r/skins/` (using the same config-dir helper used elsewhere) and pass it on the `Loader`. Without this, the drop-in story is fiction.

### D9 — Dead-code removal

While the theme package is being rewritten:
- `Styles.Table.RowAlt` — zero call sites, deleted from the new struct.
- `theme.BundledNames()` — zero call sites, not re-exported.

### D10 — Full sweep, not incremental

The following all get rewritten or replaced in one logical commit (TDD per CLAUDE.md):

```
internal/tui/theme/embed.go            (re-embed new skin set)
internal/tui/theme/loader.go           (rewrite for k9s schema)
internal/tui/theme/loader_test.go      (rewrite tests)
internal/tui/theme/schema.go           (replace with k9sSkinFile)
internal/tui/theme/styles.go           (drop RowAlt, keep rest unchanged)
internal/tui/theme/testdata/           (rewrite fixtures in k9s schema)
internal/tui/theme/skins/*.yaml        (replace with the 8 catppuccin files)
internal/tui/theme/skins/SOURCES.yaml  (new)
cmd/tui.go                             (wire UserDir)
docs/design/theming.md                 (mark superseded; point here)
docs/design/k9s-skins-dropin.md        (this file)
examples/*.yaml                        (sample with -transparent variant)
Makefile                               (skins-sync target)
```

The new commit follows the project's "one commit per logical unit" rule and ships passing tests for: every fallback path, the two-tier required set, color-value grammar (hex / default / named / invalid), shadow warning, unknown-skin fallback, and at least one end-to-end load of each bundled skin.

---

## Role mapping (full)

Required (load fails if absent):
- `body.fgColor`, `body.bgColor`

Soft-required (skin → k9s stock → fail):
- `frame.status.newColor`, `errorColor`, `addColor`, `killColor`, `highlightColor`

Cascading (skin → fallback → fallback → … → body.fg/bg):

```
header.fg/bg         ← frame.title.fgColor/bgColor → body
header.accent        ← body.logoColor → body.fg
header.ok            ← frame.status.addColor
header.warn          ← frame.status.highlightColor
header.error         ← frame.status.errorColor

table.header.fg/bg   ← views.table.header.fgColor/bgColor → body
table.header_active  ← views.table.header.sorterColor (fg) on table.header.bg
table.row.fg/bg      ← views.table.fgColor/bgColor → body
table.cursor.fg/bg   ← views.table.cursorFgColor/cursorBgColor → invert body
table.marked         ← frame.title.highlightColor on body.bg
table.dimmed         ← frame.status.completedColor → killColor → body.fg

prompt.fg/bg         ← prompt.fgColor/bgColor → body
prompt.suggestion    ← prompt.suggestColor → body.fg

crumbs.fg/bg         ← frame.crumbs.fgColor/bgColor → body
crumbs.active        ← frame.crumbs.activeColor → frame.title.highlightColor

hint.fg              ← frame.menu.fgColor → body.fg
hint.bg              ← body.bg          (k9s frame.menu has no bg)
hint.key             ← frame.menu.keyColor → body.fg
hint.help_key        ← frame.menu.numKeyColor → hint.key

modal.fg/bg          ← dialog.fgColor/bgColor → body
modal.border         ← frame.border.fgColor → body.fg

yaml.key             ← views.yaml.keyColor → body.fg
yaml.value           ← views.yaml.valueColor → body.fg
yaml.punct           ← views.yaml.colonColor → body.fg
```

"Invert body" for `table.cursor` mirrors k9s's own runtime default and means: swap `body.fg` and `body.bg` for the cursor cell.

---

## Loader behavior

Resolution order (unchanged from today):

1. `<UserDir>/<name>.yaml` — when `UserDir` set and the file exists. Shadow-warning logged if `<name>` also ships bundled.
2. embedded `skins/<name>.yaml`
3. embedded `skins/catppuccin-mocha.yaml` — fallback when `<name>` is unknown. Warning logged.

Sentinel error `ErrInvalidSkin` continues to wrap parse / mandatory-field / unresolvable-color failures so callers can still `errors.Is`. Empty skin name still short-circuits to `DefaultSkinName`.

---

## Out of scope (deferred)

- `a10r skins doctor` lint command (strict-decode + unknown-field report)
- `a10r skins list` / picker UI / runtime hot-reload — no current consumer of `BundledNames()` and no use case raised.
- Numeric ANSI palette (`color9`, `0`–`255`) values — no public skin uses them.
- Computed zebra striping — `RowAlt` has no consumers; the role is gone.
- Full k9s schema modeling for typo detection — not worth the maintenance coupling.
