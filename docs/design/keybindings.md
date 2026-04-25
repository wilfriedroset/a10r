---
title: Keybindings catalog
status: draft
audience: a10r maintainer and contributors
---

# Keybindings catalog

Authoritative list of every keybinding shipping in v0.1. Covers the seven views from the mid MVP scope (per open-question A1: alerts list, alert detail, silences list, silence form, status, receivers, groups view) plus the tenant table (per C3), and the global and table-context bindings that apply across all of them.

Decisions trace back to:

- A1, A2 — silence access pattern, silence-from-alert
- C3 — tenant selection (numeric quick-switch, `Ctrl+T` picker)
- C4 — read-only mode and dangerous-action filtering
- C5 — manual refresh
- E1 — filter syntax (`/` substring, `f` matcher post-v0.1)
- E2 — sort walk + per-view jump-sort shortcuts
- L1 — `$EDITOR` invocation for resource edits

When these conflict with the catalog below, the catalog wins and the open-question rationale is the historical record.

---

## Conventions

- `Key` — bare key.
- `Shift+Key` — shifted (uppercase letters use `Shift+X` rather than `X` to keep the table parseable).
- `Ctrl+X` — control modifier.
- `<chord>` — multi-key sequence, e.g. `gg` is `g` then `g` within the chord-timeout (default 500 ms).
- Bindings tagged **Dangerous** are filtered from the menu and ignored on press when read-only mode is active (per C4).
- Bindings tagged **Bulk** operate on rows previously marked with `Space` (or `Ctrl+A` for all visible). Pressing one with no rows marked is a no-op; a flash hints to mark first.

### Sort key convention

Per-view shortcuts of the form `Shift+<letter>` always sort by a column. They never trigger destructive or stateful actions. Bulk verbs use `Ctrl+<letter>` instead so the `Shift+letter` namespace stays sort-only and predictable across views.

### `Ctrl+S` caveat

`Ctrl+S` is bulk silence on the alerts list. Historically `Ctrl+S` is the XOFF terminal control (suspend output). Modern terminals pass it through to the application by default; if a user reports the binding does nothing, the workaround is `stty -ixon` in the parent shell.

## Precedence

When a key arrives, it is dispatched in this order; the first handler that consumes it wins:

1. **Modal** — confirm dialogs, picker overlays, help overlay.
2. **Prompt** — open `:` command bar or `/` filter input. While the prompt is open it captures every key except `Esc`.
3. **Per-view** — bindings registered by the active view.
4. **Table-context** — bindings registered when the active view embeds a table.
5. **Global** — always-available bindings.

`Esc` always reaches the modal/prompt to dismiss it; otherwise it falls through to "pop stack" at the global layer.

---

## Global

Apply everywhere except inside a modal or prompt.

| Key | Action | Notes |
| --- | --- | --- |
| `:` | Open command bar | Resolves aliases (`:alerts`, `:sil`, `:tenant`, …) |
| `/` | Open filter | Substring + matcher syntax per E1 |
| `?` | Help overlay | Lists the active view's bindings + globals |
| `r` | Refresh current view | Bypass the poll tick (per C5) |
| `Esc` | Pop stack | Dismiss modal/prompt first; otherwise pop the page stack |
| `q` | Quit | Asks for confirm if a form is dirty |
| `Ctrl+C` | Quit | Hard quit, no confirm |
| `Ctrl+T` | Tenant picker modal | Fuzzy search; `Enter` single-selects, `Space` toggles, `a` selects-all (per C3) |
| `0` | Tenant: all | Per C3 |
| `1` … `9` | Tenant: nth configured | Order from `backends:` array (per C3) |

## Table-context

Apply on every view whose body is a table (alerts, silences, receivers, tenant table, group leaf rows).

| Key | Action | Notes |
| --- | --- | --- |
| `j` / `Down` | Next row | Vim motion |
| `k` / `Up` | Previous row | Vim motion |
| `gg` / `Home` | First row | Chord (vim) |
| `Shift+G` / `End` | Last row | Vim motion |
| `Ctrl+D` / `PageDown` | Half page down | Both half-page on purpose; vim convention takes precedence over the terminal "full page" default for `PageDown`. |
| `Ctrl+U` / `PageUp` | Half page up | Both half-page (mirrors `Ctrl+D`). |
| `h` / `Left` | Previous sortable column | Walk per E2 |
| `l` / `Right` | Next sortable column | Walk per E2 |
| `Enter` | Drill | View-defined target |
| `Space` | Mark / unmark current row | Multi-select |
| `Ctrl+A` | Mark all visible rows | Multi-select |

## Per-view

### Alerts list (`:alerts`, `:al` — default view)

| Key | Action | Tag |
| --- | --- | --- |
| `s` | Silence the current alert (push form) | Dangerous |
| `Ctrl+S` | Silence all marked alerts (bulk fan-out) | Dangerous, Bulk |
| `t` | Cycle state filter: active → silenced → inhibited → all | |
| `f` | Matcher-only filter prompt | Post-v0.1 (per E1) |
| `Shift+S` | Sort by `severity` | |
| `Shift+T` | Sort by `startsAt` (time) | |
| `Shift+N` | Sort by `alertname` | |
| `Shift+R` | Sort by receiver | |
| `Shift+I` | Sort by instance | |

Navigation to the groups view goes through the command bar (`:gr` / `:groups`), not a single-key shortcut, so the `gg` chord (first row) stays unambiguous for vim users.

### Alert detail (push from a row)

| Key | Action | Tag |
| --- | --- | --- |
| `s` | Silence this alert (push form) | Dangerous |
| `y` | Copy fingerprint to clipboard | |
| `o` | Open `generatorURL` in browser | No-op if unset |
| `Esc` / `q` | Back | |

### Silences list (`:silences`, `:sil`)

| Key | Action | Tag |
| --- | --- | --- |
| `Enter` | Open silence detail (matchers, affected alerts) | |
| `n` | New silence (push empty form) | Dangerous |
| `e` | Edit silence (push form prefilled) | Dangerous |
| `Ctrl+E` | Edit silence as YAML in `$EDITOR` (per L1) | Dangerous |
| `x` / `Delete` | Expire silence (confirm modal) | Dangerous |
| `Ctrl+X` | Expire all marked silences (bulk fan-out) | Dangerous, Bulk |
| `Shift+E` | Sort by `endsAt` | |
| `Shift+S` | Sort by state (active → pending → expired) | |
| `Shift+C` | Sort by creator | |
| `Shift+T` | Sort by `startsAt` | |

### Silence form (push)

| Key | Action | Notes |
| --- | --- | --- |
| `Tab` / `Shift+Tab` | Next / previous field | huh handles this |
| `Enter` | Submit form | POST `/api/v2/silences` |
| `Esc` | Cancel | Confirm if dirty |

### Status pane (`:status`)

| Key | Action | Notes |
| --- | --- | --- |
| `j` / `k` / `Ctrl+D` / `Ctrl+U` | Scroll viewport | Vim |
| `c` | Focus the config block | Scroll-to anchor |
| `p` | Focus the peers list | Scroll-to anchor |
| `v` | Focus the version block | Scroll-to anchor |
| `Esc` / `q` | Back | |

### Receivers list (`:receivers`, `:rec`)

| Key | Action | Tag |
| --- | --- | --- |
| `Enter` | Drill to alerts filtered by this receiver | |
| `Shift+N` | Sort by name | |

### Groups view (`:groups`, `:gr`)

| Key | Action | Tag |
| --- | --- | --- |
| `Enter` | Expand / collapse group, or drill into selected alert | Context-sensitive |
| `Tab` | Force expand / collapse on the active group | |
| `s` | Silence selected (group → silence by common labels; alert → silence one) | Dangerous |

Return to the flat alerts list via the command bar (`:al` / `:alerts`) — no single-key toggle, to leave `gg` (first row) unambiguous.

### Tenant table (`:tenant`)

| Key | Action | Notes |
| --- | --- | --- |
| `Enter` | Single-select this tenant (replaces current selection) | |
| `Space` | Toggle this tenant in current selection | |
| `a` / `Ctrl+A` | Select all | `Ctrl+A` aliases `a` here so the table-context "mark all visible" muscle memory still works |
| `0` | Select all (alias of `a`, mirrors global quick-switch) | |
| `1` … `9` | Quick-switch to nth tenant | Mirrors global |

---

## Command bar aliases

Typed after `:`. The shortest form jumps to the view; the longer forms work too for users who prefer typing the full noun.

| View | Aliases |
| --- | --- |
| Alerts list | `:a` `:al` `:alert` `:alerts` |
| Alert detail | reached only by drill-down, no `:` form |
| Silences list | `:s` `:sil` `:silence` `:silences` |
| Receivers list | `:r` `:rec` `:receiver` `:receivers` |
| Groups view | `:g` `:gr` `:group` `:groups` |
| Status pane | `:st` `:status` (`:s` is taken by silences) |
| Tenant table | `:t` `:tenant` `:tenants` (or `:tenant <name\|all\|a,b>` to apply selection directly, per C3) |

Built-in commands (not view-jumps):

| Command | Action |
| --- | --- |
| `:h` `:help` | Open help overlay (same as the `?` global key, which is the canonical entry) |
| `:q` `:quit` | Quit (same as global `q`) |

## Mouse

**Disabled in v0.1.** The k9s audit flagged inconsistent mouse handling as one thing to do better; rather than ship a half-working mouse layer, v0.1 is keyboard-only. `bubblezone` (already on the shelf in `tui-library-comparison.md`) is the canonical bubbletea path when we revisit this post-v0.1.

## Stack behaviour

Each view-push grows the stack; `Esc` (or per-view `q`/`Esc` overrides) pops one frame. Concrete chains:

- **Alerts → Alert detail → Silence form**: drilling from a row pushes detail; pressing `s` on detail pushes the silence form. Submitting the form returns to the alert detail (the parent that pushed the form). Esc from detail returns to the alerts list.
- **Silences → Silence form** (via `n`/`e`/`Ctrl+E`): submit returns to the silences list. Esc from the form cancels (with confirm if dirty per J2) and returns to the same place.
- **Alerts → Groups → Alert detail**: drilling from a group's leaf into a single alert pushes detail; Esc returns to the group view at the same expansion state.
- **Tenant table → any view**: selecting a tenant from the table pops back to whatever view was active when the table was opened (it does not push a new view).

Modal overlays (confirm dialogs, tenant picker, help overlay) do not count as stack frames; Esc dismisses the modal without popping the underlying view.

## Read-only mode

Per C4, when the active backend (or the global override) sets `read_only: true`:

- Every binding tagged **Dangerous** above is hidden from the help overlay and from the J1 header hint strip.
- Pressing a hidden binding is a no-op; a flash explains "read-only mode active for `<backend>`".

## Reserved keys (do not assign in plugins later)

Plugins (deferred past v0.1; k9s pattern) must not bind these keys, since they are load-bearing TUI controls:

`:`, `/`, `?`, `Esc`, `Ctrl+C`, `Ctrl+T`, `Ctrl+S`, `Ctrl+X`, `Ctrl+E`, `Ctrl+N`, `0`–`9`, vim motions (`j` `k` `h` `l` `gg` `Shift+G`), `Ctrl+D`, `Ctrl+U`, `Enter`, `Space`, `Ctrl+A`, `r`, `q`, `Tab`, `Shift+Tab`.

`Ctrl+N` is reserved (not yet bound) for a future "compose new silence as YAML" companion to `Ctrl+E` (per L1).
