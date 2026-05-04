# Keybindings

`?` opens the in-app help overlay listing every binding active on the current view. This page is the printable companion — useful for muscle-memory practice and screen-sharing.

## Globals (always available)

| Key | What |
| --- | --- |
| `?` | Help overlay for the current view. |
| `:` | Command bar — `:alerts`, `:silences`, `:status`, `:tenant`, `:q`, etc. |
| `/` | Filter prompt — substring or matcher token over the visible rows. |
| `Esc` | Dismiss prompt / modal first; otherwise pop the page stack. |
| `q` | Quit (confirm if a form is dirty). |
| `Ctrl+C` | Hard quit, no confirm. |
| `r` | Refresh the current view (bypass the poll tick). |
| `Ctrl+T` | Tenant picker modal (fuzzy search). |
| `Ctrl+\` | Clear every mark on the focused page (alerts / silences). Silent no-op when nothing is marked. |
| `0` | Scope: all configured tenants. |
| `1` … `9` | Scope: nth tenant in `backends:` config order. |

The numeric keys (`0`-`9`) work from any page. Pressing `2` on the alerts list immediately rescopes the title to `alerts(<2nd-tenant>)[N]` and drops out-of-scope rows.

## Vim motions on every table

| Key | What |
| --- | --- |
| `j` / `↓` | Next row |
| `k` / `↑` | Previous row |
| `gg` / `Home` | First row (chord — type `g` twice within 500 ms) |
| `Shift+G` / `End` | Last row |
| `Ctrl+D` / `PageDown` | Half page down |
| `Ctrl+U` / `PageUp` | Half page up |
| `Ctrl+F` | Full page down (vim sibling of `Ctrl+D`) |
| `Ctrl+B` | Full page up (vim sibling of `Ctrl+U`) |
| `h` / `←` | Previous sortable column |
| `l` / `→` | Next sortable column |
| `Enter` | Drill into the cursor row |
| `Space` | Mark / unmark the cursor row (multi-select) |
| `Ctrl+A` | Mark every visible row |

## Sort behaviour

`Shift+<letter>` sorts by a column. Pressing the same shortcut twice flips ASC↔DESC. The active column shows an `↑` (ASC) or `↓` (DESC) arrow next to its uppercase header label — that's the source of truth.

Switching to a new column resets to that column's *default* direction. Severity defaults to descending (worst-first); everything else defaults to ascending.

## Per-view shortcuts

### Alerts list

| Key | What |
| --- | --- |
| `s` | Silence. With no marks: silences the cursor alert (single form). With one or more marks (toggled via `Space`): bulk silence — the form opens once, then `a10r` fans out one CreateSilence per marked alert (per-alert labels, uniform comment / start / end). |
| `t` | Cycle state filter: active → silenced → inhibited → all |
| `Shift+S` | Sort by severity |
| `Shift+T` | Sort by start time |
| `Shift+N` | Sort by alertname |
| `Shift+R` | Sort by receiver |
| `Shift+I` | Sort by instance |

### Alert detail

| Key | What |
| --- | --- |
| `s` | Silence this alert |
| `y` | Copy fingerprint to clipboard |
| `o` | Open `generatorURL` in the default browser |
| `Esc` | Back |

### Silences list

| Key | What |
| --- | --- |
| `Enter` | Open silence detail (read-only YAML) |
| `n` | New silence (empty form) |
| `e` | Edit silence (form prefilled) |
| `Ctrl+E` | Edit silence as YAML in `$EDITOR` |
| `x` / `Delete` | Expire. With no marks: expires the cursor silence after a default-No confirm. With one or more marks: bulk expire — confirm wording counts the queued silences and breaks them down per tenant (`(tenant prod=12, staging=3)`); fanout retries failed targets only. |
| `Shift+E` | Sort by `endsAt` |
| `Shift+S` | Sort by state |
| `Shift+C` | Sort by creator |
| `Shift+T` | Sort by `startsAt` |

### Silence detail

| Key | What |
| --- | --- |
| `j` / `k` | Scroll down / up one line |
| `Ctrl+D` / `Ctrl+U` | Half-page down / up |
| `Ctrl+F` / `Ctrl+B` | Full-page down / up |
| `G` / `gg` | Jump to last / first line |
| `Esc` | Back |

### Silence form

| Key | What |
| --- | --- |
| `Tab` / `Shift+Tab` | Next / previous field |
| `Enter` | Submit |
| `Esc` | Cancel (confirm if dirty) |

### Status pane

| Key | What |
| --- | --- |
| `j` `k` `Ctrl+D` `Ctrl+U` `Ctrl+F` `Ctrl+B` | Scroll the viewport |
| `c` | Jump to the cluster section |
| `v` | Jump to the version block |
| `p` | Jump to the raw config block |
| `Esc` / `q` | Back |

### Receivers / Groups / Tenant table

The lists follow the same vim motions as alerts/silences. View-specific verbs:

| View | Key | What |
| --- | --- | --- |
| Receivers | `Enter` | Drill to alerts filtered by this receiver |
| Groups | `Enter` | Expand/collapse the group, or drill into a leaf alert |
| Groups | `Tab` | Force-expand / collapse the active group |
| Groups | `s` | Silence the group by its common-labels intersection |
| Tenant | `Enter` | Single-select the cursor row |
| Tenant | `Space` | Toggle the cursor row in the selection |
| Tenant | `a` / `Ctrl+A` | Select every tenant |

## Read-only mode

`--read-only` (or `read_only: true` in the config) hides every dangerous binding above. They stop responding and stop appearing in `?` and the right-hand hint strip — so a stray `s` or `x` during a screenshare can't fire by accident.

## Conventions you'll spot in the chrome

- **Title `<resource>(<scope>)[<count>]`.** The bordered panel's title shows what you're looking at. `(<scope>)` is the active tenant set; `[<count>]` is filtered/total when a filter is on, otherwise the total.
- **Cursor row** keeps the body background and brightens the foreground.
- **Marked rows** (after `Space`) tint the foreground only — different colour from the cursor so you can tell them apart at a glance.
- **`TENANT` column** appears on alerts when more than one tenant is in scope. Switching to a single-tenant scope hides it.
- **Bold breadcrumbs** in the footer trace the page stack: `<alerts> <detail> <silence>`. `Esc` pops one frame.
