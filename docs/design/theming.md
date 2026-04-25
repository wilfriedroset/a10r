---
title: Theming and skin schema
status: draft
audience: a10r maintainer, theme authors, contributors
---

# Theming and skin schema

a10r ships colour themes as YAML "skin" files. The schema is two-layer: a per-theme **palette** of named colours, then a fixed-shape **roles** map that binds semantic slots in the UI to palette entries. v0.1 ships three bundled themes; users can drop additional skins under `<config-dir>/skins/`.

Decisions on scope and bundle live in open-question M1; the schema below is the authoritative reference.

---

## File layout

```yaml
# Required: theme metadata.
name: catppuccin-mocha          # must match the file basename
description: Catppuccin Mocha (dark)
author: catppuccin authors

# Required: palette. Names are private to this theme — pick whatever
# the upstream palette uses. They are referenced from the roles map below.
palette:
  base:    "#1e1e2e"
  surface: "#313244"
  text:    "#cdd6f4"
  subtext: "#a6adc8"
  overlay: "#6c7086"
  red:     "#f38ba8"
  peach:   "#fab387"
  yellow:  "#f9e2af"
  green:   "#a6e3a1"
  sky:     "#89dceb"
  blue:    "#89b4fa"
  lavender: "#b4befe"
  mauve:   "#cba6f7"

# Required: roles. The keys here are a fixed public contract — every theme
# must define every role. Values reference palette names defined above.
roles:
  body:
    fg: text
    bg: base

  header:
    fg: text
    bg: base
    accent: blue
    ok: green
    warn: yellow
    error: red

  table:
    header:        { fg: subtext, bg: base }
    header_active: { fg: base, bg: blue }
    row:           { fg: text, bg: base }
    row_alt:       { fg: text, bg: surface }
    cursor:        { fg: base, bg: lavender }
    marked:        { fg: base, bg: peach }
    dimmed:        { fg: overlay, bg: base }

  severity:
    critical: red
    warning:  yellow
    info:     sky
    unknown:  overlay

  silence_state:
    active:  green
    pending: yellow
    expired: overlay

  prompt:
    fg: text
    bg: base
    suggestion: overlay

  flash:
    success: green
    info:    sky
    warn:    yellow
    error:   red

  crumbs:
    fg: subtext
    bg: base
    active: peach

  hint:
    fg: subtext
    bg: base
    key: peach
    help_key: green

  modal:
    fg: text
    bg: base
    border: overlay

  yaml:
    key:   blue
    value: text
    punct: overlay
```

## Palette

- Every key is a string; values are 6-digit hex colours (`#rrggbb`). 8-digit hex (with alpha) is not supported by terminal renderers — the loader rejects it.
- Names are **theme-private**. catppuccin uses `peach` and `mauve`; gruvbox uses `orange` and `purple`. The role map below references whatever names the palette declares.
- A theme that needs a colour outside its declared palette should add it to the palette block rather than inlining a hex literal in `roles`. This keeps the palette block authoritative and easy to fork.

## Roles (the public contract)

Every theme **must** define every role; loading fails with a clear error if any are missing. New roles added in future a10r versions must come with a default value derived from existing roles so existing themes keep loading.

| Role | Sub-keys | Where it shows |
| --- | --- | --- |
| `body` | `fg`, `bg` | Default text and background everywhere not overridden |
| `header` | `fg`, `bg`, `accent`, `ok`, `warn`, `error` | Top-of-screen header band (per J1). `accent` for tenant name and counts; `ok`/`warn`/`error` for backend connection state (per C2) |
| `table.header` | `fg`, `bg` | Column-name row above each table |
| `table.header_active` | `fg`, `bg` | The currently sorted column in the header (per E2 and I4) |
| `table.row` | `fg`, `bg` | Default row |
| `table.row_alt` | `fg`, `bg` | Alternating row (zebra striping; renderer toggles) |
| `table.cursor` | `fg`, `bg` | Currently selected row |
| `table.marked` | `fg`, `bg` | Rows marked via `Space` for bulk actions (per keybindings catalog) |
| `table.dimmed` | `fg`, `bg` | Stale data when backend is unreachable (per C2) and rows hidden by read-only mode (per C4) |
| `severity` | `critical`, `warning`, `info`, `unknown` | Per-row colouring on the alerts list, keyed on the alert's `severity` label |
| `silence_state` | `active`, `pending`, `expired` | Per-row colouring on the silences list |
| `prompt` | `fg`, `bg`, `suggestion` | Bottom-strip `:` and `/` prompt and inline suggestions |
| `flash` | `success`, `info`, `warn`, `error` | Bottom-strip ephemeral messages |
| `crumbs` | `fg`, `bg`, `active` | Breadcrumb strip |
| `hint` | `fg`, `bg`, `key`, `help_key` | Header right-zone keybinding hint strip (per J1). `key` is the highlighted shortcut letter; `help_key` is the always-on `?` indicator |
| `modal` | `fg`, `bg`, `border` | Confirm dialogs, tenant picker, help overlay |
| `yaml` | `key`, `value`, `punct` | Status pane raw YAML viewer (per I1) and Mimir config editor (post-v0.1) |

## Resolution

When `theme.name: <name>` is set in `a10r.yaml` (or `--theme <name>` is passed):

1. Look for `<config-dir>/skins/<name>.yaml`. If present, load it. A startup warning is logged when the user file shadows a bundled theme of the same name.
2. Otherwise look for the bundled theme `<name>` in the embedded `skins/` directory.
3. If neither exists, fall back to `catppuccin-mocha` and surface an error flash on first render.

Names are case-sensitive and must match the file basename without `.yaml`.

## Selection

Config:

```yaml
theme:
  name: catppuccin-mocha
```

CLI override:

```
a10r --theme gruvbox-dark
```

Precedence follows K1: CLI flag → config field → built-in default (`catppuccin-mocha`).

## Bundled themes (v0.1)

Embedded in the binary via `embed.FS`; available without any user file.

| Name | Mode | Source palette |
| --- | --- | --- |
| `catppuccin-mocha` | dark (default) | [catppuccin/catppuccin](https://github.com/catppuccin/catppuccin) — Mocha flavour |
| `catppuccin-latte` | light | catppuccin — Latte flavour |
| `gruvbox-dark` | dark | [morhetz/gruvbox](https://github.com/morhetz/gruvbox) — dark medium |

Adding a bundled theme post-v0.1 requires only a new YAML file under the embedded `skins/` directory and a one-line addition to the inventory.

## Authoring a custom skin

1. Copy a bundled theme as a starting point: `cp /path/to/embedded/catppuccin-mocha.yaml <config-dir>/skins/my-theme.yaml`.
2. Edit `name:` to match the new file basename.
3. Adjust `palette:` entries; rename or add palette keys freely.
4. Re-point `roles:` entries to the new palette names.
5. Set `theme.name: my-theme` in `a10r.yaml` (or `--theme my-theme` on the CLI). Restart a10r.

The schema validator runs on every load and reports missing roles, undefined palette references, and malformed colours with line/column.

## Renderer

Themes compile once at load time into a struct of `lipgloss.Style` instances keyed by role. Views consume styles by role name (`styles.Table.Cursor`, `styles.Severity.Critical`, etc.) and never reach for raw palette entries — that indirection is what makes a theme swap cheap.

Lipgloss adaptive colours are **not** used. Each theme is one fixed palette; light/dark switching happens by selecting a different theme.

## Future (post-v0.1)

- **Live reload** via `fsnotify` on `<config-dir>/skins/`. On file change, recompile styles and emit a `themeChangedMsg` to trigger a re-render. Bundled themes are static; only user files are watched.
- **Adaptive mode** (`theme.name: auto`) that picks between a paired light and dark theme based on the terminal's reported background. Requires a small light/dark pairing config.
- **Per-backend theme overrides** for users who want the prod cluster to render in red and staging in green. Likely lives under each `backends:` entry as `theme: <name>`.
