# Keybindings

`?` opens the in-app help overlay listing every binding active on the current view. This page is the printable companion — useful for muscle-memory practice and screen-sharing.

## Globals (always available)

| Key | What |
| --- | --- |
| `?` | Help overlay for the current view. |
| `:` | Command bar — `:alerts`, `:silences`, `:status`, `:tenant`, `:q`, etc. The help overlay paints this chip as `<:cmd>  Command mode` so the colon-then-command shape reads at a glance. As you type, the alphabetically-first matching alias trails your input as a dim ghost; `Tab` (or `Ctrl+F`) accepts it. Typed input is bolded so it stays visually distinct from the ghost suffix. |
| `/` | Filter prompt — autodetects substring / fuzzy / literal / regex from the buffer (see [Filter modes](#filter-modes) below). |
| `Esc` | Dismiss prompt / modal first; otherwise pop the page stack. |
| `q` | Quit (confirm if a form is dirty). |
| `Ctrl+C` | Hard quit, no confirm. |
| `r` | Refresh the current view (bypass the poll tick). |
| `Ctrl+T` | Tenant picker modal (fuzzy search). |
| `Ctrl+\` | Clear every mark on the focused page (alerts / silences). Silent no-op when nothing is marked. |
| `0` | Scope: all configured tenants. |
| `1` … `9` | Scope: nth tenant in `backends:` config order. |

The numeric keys (`0`-`9`) work from any page. Pressing `2` on the alerts list immediately rescopes the title to `alerts(<2nd-tenant>)[N]` and drops out-of-scope rows.

## Filter modes

The `/` prompt classifies its input into one of four matcher modes — there is no "switch the mode" key, the buffer itself decides:

| Buffer | Mode | When |
| --- | --- | --- |
| `~<text>` | fuzzy | Leading `~`. The `~` is stripped before matching; the rest is fed to a fuzzy matcher. |
| `\<text>` | literal | Leading `\`. The `\` is stripped; the rest is matched as a plain substring. Use this as the escape hatch when your search would otherwise look like a regex (e.g. `\(prod)`). |
| `<text>` with two or more distinct regex metacharacters from `. * + ? [ ] ( ) \| ^ $ \` | regex | The body is compiled as a Go regular expression. |
| anything else | substring | Default — case-insensitive substring over the row's full search corpus (see below). |

The two-meta threshold is deliberate. `web.api`, `1.2.3.4`, `abc*` keep the substring default — a single `.` or `*` is the most common false-flag in alert filtering. `web.*api`, `^web`, `(prod\|stg)` flip immediately. If you want the literal text and the body trips the threshold, prefix with `\`.

### What `/` actually matches against

The match scope is wider than the visible columns by design — operators want to filter by attributes that aren't always in the table:

- **Alerts list:** every label value (`alertname`, `severity`, `instance`, `cluster`, …) AND every annotation value (`summary`, `description`, runbook URLs). A `/api` search on the alerts page can therefore hit an alert whose `summary` annotation reads "API latency above SLO" even though the alertname is `HighLatency`. Fuzzy mode (`~`) over this corpus is intentionally lenient — it's how you discover an alert when you only remember a fragment of a label nobody put in the name.
- **Silences list:** silence ID, creator, comment, state, and every matcher's `name`/`value`.
- **Groups list:** the group's collapsed label set plus the alertname of each leaf.
- **Receivers list:** receiver name (single-axis).

If a fuzzy/substring search surfaces matches that look unrelated to the alertname, the hit is almost always an annotation or a non-name label. Use literal mode (`\<text>`) for exact substring matching with no escaping, or regex mode (e.g. `^web` — note the alerts page's composite is unordered so anchors won't reliably pin "alertname only"). Narrowing the default scope to alertname-only is on the backlog for a future release.

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

The mouse wheel walks the cursor too — wheel-up is the same as `k`, wheel-down the same as `j`. Wheel ticks on the open `?` overlay scroll the help body so a long binding list stays reachable. Click and drag are intentionally unbound; the rest of the surface stays keyboard-driven.

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
| `Shift+N` | Sort by alertname |
| `Shift+T` | Sort by state |
| `Shift+A` | Sort by age |

### Alert detail

| Key | What |
| --- | --- |
| `s` | Silence this alert |
| `y` | Toggle raw alert payload as YAML (k9s-style escape hatch). The title appends ` [raw yaml]` while raw mode is active so the two views are visually distinguishable at a glance. |
| `c` | Copy fingerprint to clipboard |
| `o` | Open `generatorURL` in the default browser |
| `Esc` | Back |

### Silences list

| Key | What |
| --- | --- |
| `Enter` | Open silence detail (read-only YAML) |
| `n` | New silence (empty form) |
| `e` | Edit silence (form prefilled) |
| `Ctrl+E` | Edit silence as YAML in `$EDITOR` |
| `Ctrl+N` | Recreate the cursor silence (only on expired rows). The form lands prefilled with the matchers and comment from the source silence; creator is your current user, start defaults to now, and the cursor focuses the `Ends` line so you can type a fresh duration. Submits as a new silence (new ID); the original expired silence is left untouched. Refuses on active or pending rows — use `e` to extend a live silence. |
| `x` / `Delete` | Expire. With no marks: expires the cursor silence after a default-No confirm. With one or more marks: bulk expire — confirm wording counts the queued silences and breaks them down per tenant (`(tenant prod=12, staging=3)`); fanout retries failed targets only. |
| `Shift+E` | Sort by `endsAt` |
| `Shift+S` | Sort by `startsAt` |
| `Shift+C` | Sort by creator |
| `Shift+T` | Sort by state |

### Silence detail

| Key | What |
| --- | --- |
| `y` | Toggle raw silence payload as YAML (k9s-style escape hatch); structured curated view by default. The title appends ` [raw yaml]` while raw mode is active so the two YAML views are visually distinguishable at a glance. |
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
| Receivers | `Shift+N` | Toggle the name sort ASC↔DESC (single sortable axis; `h`/`l` are no-ops here) |
| Groups | `Enter` | Expand/collapse the group, or drill into a leaf alert |
| Groups | `Tab` | Force-expand / collapse the active group |
| Groups | `s` | Silence the group by its common-labels intersection |
| Groups | `Shift+N` | Sort by group name (label-set) |
| Groups | `Shift+C` | Sort by alert count |
| Groups | `Shift+V` | Sort by worst severity in the group |
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
