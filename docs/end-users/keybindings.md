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
| `t` | Toggle timestamps between relative (`5m ago`) and absolute (ISO local) — app-wide. |
| `Ctrl+T` | Tenant picker modal (fuzzy search). |
| `Ctrl+\` | Clear every mark on the focused page (alerts / silences). Silent no-op when nothing is marked. |
| `0` | Scope: all configured tenants. |
| `1` … `9` | Scope: nth tenant in `backends:` config order. |

The numeric keys (`0`-`9`) work from any page. Pressing `2` on the alerts list immediately rescopes the title to `alerts(<2nd-tenant>)[N]` and drops out-of-scope rows.

## Filter modes

The `/` prompt classifies its input by the buffer itself — there is no "switch the mode" key:

| Buffer | Mode | When |
| --- | --- | --- |
| `name<op>value` (`=`, `!=`, `=~`, `!~`) | label matcher | **Alerts list & group detail only.** A Prometheus-style label selector (e.g. `cluster_id=99`, `cluster_id=~9.*`, `severity!=info`) filters by that exact label — key-scoped, not a value substring. Combine several with `,` (or `&&`) to AND them: `cluster_id=99,role=consul`. Quote a value to keep a literal `,` inside a regex: `cluster_id=~"(a,b)"`. Checked before the modes below; prefix with `\` to force a plain substring instead. |
| `~<text>` | fuzzy | Leading `~`. The `~` is stripped before matching; the rest is fed to a fuzzy matcher. |
| `\<text>` | literal | Leading `\`. The `\` is stripped; the rest is matched as a plain substring. Use this as the escape hatch when your search would otherwise look like a regex (e.g. `\(prod)`) or a label matcher (e.g. `\foo=bar`). |
| `<text>` with two or more distinct regex metacharacters from `. * + ? [ ] ( ) \| ^ $ \` | regex | The body is compiled as a Go regular expression. |
| anything else | substring | Default — case-insensitive substring over the row's full search corpus (see below). |

The label-matcher operators mirror the silence form: `=` exact, `!=` not-equal (also matches instances missing the label), `=~` / `!~` fully-anchored regex. The two-meta threshold for the regex mode is deliberate. `web.api`, `1.2.3.4`, `abc*` keep the substring default — a single `.` or `*` is the most common false-flag in alert filtering. `web.*api`, `^web`, `(prod\|stg)` flip immediately. If you want the literal text and the body trips the threshold, prefix with `\`.

### What `/` actually matches against

The match scope is wider than the visible columns by design — operators want to filter by attributes that aren't always in the table:

- **Alerts list:** in text mode, every label value (`alertname`, `severity`, `instance`, `cluster`, …) AND every annotation value (`summary`, `description`, runbook URLs) — so `/api` can hit an alert whose `summary` reads "API latency above SLO" even though the alertname is `HighLatency`. In label-matcher mode (`name<op>value`) it filters by the exact label instead; filtering narrows the underlying instances and the page regroups, so COUNT / STATE reflect the survivors.
- **Group detail (instance list):** same as the alerts list — text mode searches each instance's label/annotation values; `cluster_id=99` filters that instance set by the exact label.
- **Silences list:** silence ID, creator, comment, state, and every matcher's `name`/`value`.
- **Groups list:** the group's collapsed label set plus the alertname of each leaf.
- **Receivers list:** receiver name (single-axis).

If a fuzzy/substring search surfaces matches that look unrelated to the alertname, the hit is almost always an annotation or a non-name label. To scope to a specific label instead — including the alertname itself — use the label-matcher mode on the alerts list / group detail (e.g. `alertname=HighCPU`, `alertname=~Hi.*`, `severity!=info`). For exact-substring text matching with no escaping, use literal mode (`\<text>`).

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

Rows are **alerts** — one per `(tenant, alertname)` — each carrying a COUNT of instances and a per-state breakdown (active / suppressed / unprocessed). `Enter` drills by size: a single-instance alert (flagged with a trailing `→` in the COUNT column) opens the instance detail directly; a multi-instance alert opens the group-detail instance list. Filters narrow the underlying instances and then regroup, so COUNT / STATE / AGE always describe what's on screen.

| Key | What |
| --- | --- |
| `Enter` | Drill: single-instance alert → instance detail; multi-instance alert → group detail. |
| `s` | Silence the whole alert (`alertname=` matcher only). No marks: prefilled form — a confirm guards alerts with more than one instance, and a scope note warns that any active filter is *not* applied. With marks (`Space`): bulk — one silence per marked alert. |
| `/` | Substring filter over the instances. |
| `Shift+F` | Cycle the state filter: active → suppressed → unprocessed → all. |
| `Shift+T` | Toggle the STATE breakdown between full (`9 active · 3 suppressed`) and compact (`9ac 3su`) — app-wide. |
| `Shift+S` | Sort by severity (worst in the group). |
| `Shift+N` | Sort by alertname. |
| `Shift+C` | Sort by instance count. |
| `Shift+A` | Sort by age (oldest instance). |

### Group detail

The instance list for one alert, reached by `Enter` on a multi-instance row. Rows are individual **alert instances**; each shows only the labels that distinguish it, while the labels every instance shares appear once in a common-labels strip above the table. `h`/`l` walk the sort columns; severity is the default sort (it has no `Shift+S` shortcut here — that would collide with `S`).

| Key | What |
| --- | --- |
| `Enter` | Drill into the cursor instance (instance detail). |
| `s` | Silence the cursor instance (full labels). With marks: bulk — one silence per marked instance; at 10+ marks a warning suggests silencing the whole alert instead. |
| `S` | Open the silences suppressing this alert's instances. |
| `Shift+C` | Show / hide the common-labels strip. |
| `/` | Substring filter. |
| `Shift+F` | Cycle the state filter. |
| `Shift+T` | Toggle the STATE rendering (full / compact). |
| `Shift+N` | Sort by instance labels. |
| `Shift+A` | Sort by age. |

### Alert detail (instance detail)

One fully-expanded instance — its labels, annotations, generator URL, and suppression block. Reached from the alerts list (single-instance alert) or from the group detail.

| Key | What |
| --- | --- |
| `s` | Silence this instance (full labels). |
| `S` | Open the silences suppressing this instance. |
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
| `Enter` | Submit (from any single-line field). On the Tenant row it opens the tenant picker; in the Matchers box it inserts a newline for multi-matcher entry. |
| `Ctrl+S` | Submit from any field, including the Matchers box |
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
- **Bold breadcrumbs** in the footer trace the page stack: `<alerts> <instances> <detail>` (a single-instance alert skips straight to `<detail>`). `Esc` pops one frame.
