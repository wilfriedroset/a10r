# Keybindings

`?` opens the in-app help overlay listing every binding active on the current view. This page is the printable companion — useful for muscle-memory practice and screen-sharing.

## Globals (always available)

| Key | What |
| --- | --- |
| `?` | Help overlay for the current view. |
| `:` | Command bar — `:alerts`, `:silences`, `:status`, `:info`, `:config`, `:skin`, `:reload`, `:tenant`, `:q` (or `:quit`), etc. The help overlay paints this chip as `<:cmd>  Command mode` so the colon-then-command shape reads at a glance. As you type, the alphabetically-first matching alias trails your input as a dim ghost; `Tab` (or `Ctrl+F`) accepts it. Typed input is bolded so it stays visually distinct from the ghost suffix. |
| `/` | Filter prompt — autodetects substring / fuzzy / literal / regex from the buffer (see [Filter modes](#filter-modes) below). |
| `Esc` | Dismiss prompt / modal first, then an open `Shift+V` range; otherwise pop the page stack. |
| `q` | Quit (confirm if a form is dirty). |
| `Ctrl+C` | Hard quit, no confirm. |
| `r` | Refresh the current view (bypass the poll tick). |
| `t` | Toggle timestamps between relative (`5m ago`) and absolute (ISO local) — app-wide. |
| `Ctrl+T` | Tenant picker modal (fuzzy search). |
| `Ctrl+\` | Clear every mark on the focused page (alerts / group detail / silences) and cancel any open `Shift+V` range. Flashes only when marks were cleared: cancelling a range on its own is silent, and the `visual` chip leaving the title is the cue. |
| `0` | Scope: all configured tenants. |
| `1` … `9` | Scope: nth tenant in `backends:` config order. |

The numeric keys (`0`-`9`) work from any page. Pressing `2` on the alerts list immediately rescopes the title to `alerts(<2nd-tenant>)[N]` and drops out-of-scope rows.

## Filter modes

The `/` prompt classifies its input by the buffer itself — there is no "switch the mode" key:

| Buffer | Mode | When |
| --- | --- | --- |
| `a \|\| b`, `!a`, `count>3` | expr | **Alerts list & group detail only.** A boolean expression over the modes below, built from `&&`, `\|\|`, `!` and parentheses, plus the typed keys `count`, `age` and `state`. See [Boolean expressions](#boolean-expressions). Prefix the buffer with `\` to force a plain substring instead. |
| `name<op>value` (`=`, `!=`, `=~`, `!~`) | label matcher | **Alerts list & group detail only.** A Prometheus-style label selector (e.g. `cluster_id=99`, `cluster_id=~9.*`, `severity!=info`) filters by that exact label — key-scoped, not a value substring. Combine several with `,` (or `&&`) to AND them: `cluster_id=99,role=consul`. Quote a value to keep a literal `,` inside a regex: `cluster_id=~"(a,b)"`. Checked before the modes below; prefix with `\` to force a plain substring instead. |
| `~<text>` | fuzzy | Leading `~`. The `~` is stripped before matching; the rest is fed to a fuzzy matcher. |
| `\<text>` | literal | Leading `\`. The `\` is stripped; the rest is matched as a plain substring. Use this as the escape hatch when your search would otherwise look like a regex (e.g. `\(prod)`) or a label matcher (e.g. `\foo=bar`). |
| `<text>` with two or more distinct regex metacharacters from `. * + ? [ ] ( ) \| ^ $ \` | regex | The body is compiled as a Go regular expression. |
| anything else | substring | Default — case-insensitive substring over the row's full search corpus (see below). |

The label-matcher operators mirror the silence form: `=` exact, `!=` not-equal (also matches instances missing the label), `=~` / `!~` fully-anchored regex. The two-meta threshold for the regex mode is deliberate. `web.api`, `1.2.3.4`, `abc*` keep the substring default — a single `.` or `*` is the most common false-flag in alert filtering. `web.*api`, `^web`, `(prod\|stg)` flip immediately. If you want the literal text and the body trips the threshold, prefix with `\`.

The characters that made a row match are painted in the skin's filter colour (`frame.title.filterColor`); on the cursor row, a marked row and a dimmed row they are underlined instead, so the row keeps its own colour. A row kept by a match the table does not show — an annotation, a hidden label — is listed without any painted characters, and a label matcher paints nothing at all, because it matches on label structure rather than on the rendered text. An expression paints nothing either, because its matching characters are spread across terms the highlighter cannot attribute.

When the buffer will not compile — a half-typed `^web(`, a label matcher whose `=~` value is malformed, or an expression that ends on a dangling `||` — the title tag reads `[regex: <reason>]`, `[matcher: <reason>]` or `[expr: <reason>]` instead of the mode name, the rows stay on the last good filter, and `Enter` keeps the prompt open so you can fix the buffer. `Esc` still restores the filter you had before the prompt opened.

### Boolean expressions

**Alerts list & group detail only.** Combine the modes above with boolean operators. While the prompt is open, the title tag reads `[expr]` for a buffer the expression path owns, in place of the five-mode label.

| Operator | Spelling | Example |
| --- | --- | --- |
| AND | `&&`, `,`, or a space | `severity=critical team=infra` |
| OR | `\|\|` | `severity=critical \|\| severity=warning` |
| NOT | `!` | `!severity=info` |
| grouping | `(` `)` | `(severity=info \|\| team=ops) && age>1h` |

`!` binds tighter than AND, and AND binds tighter than OR, so `count>=5 && !severity=info \|\| age<2h` reads as `(count>=5 && !severity=info) \|\| (age<2h)`. Each operand is one of the modes above — label matcher, fuzzy, literal, regex or substring.

Three typed keys compare against a value the table shows rather than against the search text:

| Key | Values | Example |
| --- | --- | --- |
| `count` | an integer, the alert's COUNT | `count>=5` |
| `age` | a duration in `s` `m` `h` `d` `w`, largest unit first (`30m`, `2h`, `1h30m`, `3d`) | `age>2h` |
| `state` | `active`, `suppressed`, `unprocessed` | `state=suppressed` |

All six comparison operators work on them: `=`, `!=`, `>`, `>=`, `<`, `<=`. The regex operators `=~` and `!~` do not. Key names are case-insensitive. `count` is the group size **before** the expression runs, that is, after the scope and the `Shift+F` state filter only, so `count>=5` asks "this alert has at least 5 instances in scope", not "at least 5 of them also match the rest of my buffer". On group detail `count` is undefined, and a term over it matches nothing, negated or not. The same rule covers `age` on an instance with no start time.

A buffer becomes an expression only when it carries a `||`, a `!`, a typed comparison, or a `(` next to an explicit `&&` or `,`. Everything else keeps its old meaning: `a=1,b=2` stays the label-matcher AND chain it always was, and `(web|api)` stays a regex alternation. To use a regex that carries parentheses inside an expression, wrap it in slashes: `!/(web|api)/ && age>1h`. A leading `\` still forces literal mode over the whole buffer.

### What `/` actually matches against

The match scope is wider than the visible columns by design — operators want to filter by attributes that aren't always in the table:

- **Alerts list:** in text mode, every label value (`alertname`, `severity`, `instance`, `cluster`, …) AND every annotation value (`summary`, `description`, runbook URLs) — so `/api` can hit an alert whose `summary` reads "API latency above SLO" even though the alertname is `HighLatency`. In label-matcher mode (`name<op>value`) it filters by the exact label instead; filtering narrows the underlying instances and the page regroups, so COUNT / STATE reflect the survivors.
- **Group detail (instance list):** same as the alerts list — text mode searches each instance's label/annotation values; `cluster_id=99` filters that instance set by the exact label.
- **Silences list:** silence ID, creator, comment, state, and every matcher's `name`/`value`.
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
| `h` / `←` | Previous sortable column. On the alerts list and on group detail `←` scrolls the columns instead, so `h` alone walks the sort there. |
| `l` / `→` | Next sortable column. On the alerts list and on group detail `→` scrolls the columns instead, so `l` alone walks the sort there. |
| `Enter` | Drill into the cursor row |
| `Space` | Mark / unmark the cursor row (multi-select) — on the pages that have marks (alerts, group detail, silences) and on the tenant table |

The mouse wheel walks the cursor too — wheel-up is the same as `k`, wheel-down the same as `j`. Wheel ticks on the open `?` overlay scroll the help body so a long binding list stays reachable. Click and drag are intentionally unbound; the rest of the surface stays keyboard-driven.

## Sort behaviour

On a list page, `Shift+<letter>` sorts by a column. Pressing the same shortcut twice flips ASC↔DESC. The active column shows an `↑` (ASC) or `↓` (DESC) arrow next to its uppercase header label — that's the source of truth.

Switching to a new column resets to that column's *default* direction. Severity defaults to descending (worst-first); everything else defaults to ascending.

A label column you declare in the configuration joins the same set. Give it a `sort_key` and `Shift+<that letter>` sorts by it, `h`/`l` walk onto it, and the help overlay lists it. A column with no `sort_key` still renders, but nothing sorts by it. Rows whose cell is empty sort last in both directions. A `<N values>` rollup marker ranks after every plain value, so it lands at the end ascending and at the front descending. Group-detail rows are single instances, so they carry no marker. A `wide` column is not a sort axis while it is out of view: `Shift+<letter>` does nothing and `h`/`l` step over it. A sort you already made on one is parked, not lost. The page falls back to its default sort and direction, then restores your choice when `Shift+W` brings the column back. See [configuration.md](configuration.md#label-columns).

## Per-view shortcuts

### Alerts list

Rows are **alerts** — one per `(tenant, alertname)` — each carrying a COUNT of instances and a per-state breakdown (active / suppressed / unprocessed). `Enter` drills by size: a single-instance alert (flagged with a trailing `→` in the COUNT column) opens the instance detail directly; a multi-instance alert opens the group-detail instance list. Filters narrow the underlying instances and then regroup, so COUNT / STATE / AGE always describe what's on screen.

| Key | What |
| --- | --- |
| `Enter` | Drill: single-instance alert → instance detail; multi-instance alert → group detail. |
| `s` | Silence the whole alert (`alertname=` matcher only). No marks: prefilled form — a confirm guards alerts with more than one instance, and a scope note warns that any active filter is *not* applied. With marks (`Space`): bulk — one silence per marked alert. |
| `Shift+V` | Start a mark range: anchor on the cursor row, walk with `j`/`k`, then `Space` or a second `Shift+V` marks every row in between. `Esc` cancels. |
| `/` | Substring filter over the instances. |
| `Shift+F` | Cycle the state filter: active → suppressed → unprocessed → all. |
| `Shift+T` | Toggle the STATE breakdown between full (`9 active · 3 suppressed`) and compact (`9ac 3su`) — app-wide. |
| `Shift+W` | Show or hide the label columns you declared `wide: true`. Listed only when the page has one. |
| `Shift+S` | Sort by severity (worst in the group). |
| `Shift+N` | Sort by alertname. |
| `Shift+C` | Sort by instance count. |
| `Shift+A` | Sort by age (oldest instance). |
| `←` / `→` | Scroll the columns when the row is too wide for the terminal. The first data column stays pinned, and the header marks the cut edge with `<` or `>`. On a terminal wide enough for every column the two keys do nothing, and `h` / `l` keep the sort walk either way. |

### Group detail

The instance list for one alert, reached by `Enter` on a multi-instance row. Rows are individual **alert instances**; each shows only the labels that distinguish it, while the labels every instance shares appear once in a common-labels strip above the table. `h`/`l` walk the sort columns; severity is the default sort (it has no `Shift+S` shortcut here — that would collide with `S`).

| Key | What |
| --- | --- |
| `Enter` | Drill into the cursor instance (instance detail). |
| `s` | Silence the cursor instance (full labels). With marks: bulk — one silence per marked instance; at 10+ marks a warning suggests silencing the whole alert instead. |
| `Shift+V` | Start a mark range: anchor on the cursor row, walk with `j`/`k`, then `Space` or a second `Shift+V` marks every row in between. `Esc` cancels. |
| `S` | Open the silences suppressing this alert's instances. |
| `Shift+C` | Show / hide the common-labels strip. |
| `/` | Substring filter. |
| `Shift+F` | Cycle the state filter. |
| `Shift+T` | Toggle the STATE rendering (full / compact). |
| `Shift+W` | Show or hide the label columns you declared `wide: true`. Listed only when the page has one. |
| `Shift+N` | Sort by instance labels. |
| `Shift+A` | Sort by age. |
| `←` / `→` | Scroll the columns when the row is too wide for the terminal. SEVERITY stays pinned, and the header marks the cut edge with `<` or `>`. On a terminal wide enough for every column the two keys do nothing, and `h` / `l` keep the sort walk either way. |

### Alert detail (instance detail)

One fully-expanded instance — its labels, annotations, generator URL, and suppression block. Reached from the alerts list (single-instance alert) or from the group detail.

| Key | What |
| --- | --- |
| `s` | Silence this instance (full labels). |
| `S` | Open the silences suppressing this instance. |
| `y` | Toggle raw alert payload as YAML (k9s-style escape hatch). The title appends ` [raw yaml]` while raw mode is active so the two views are visually distinguishable at a glance. |
| `c` | Copy fingerprint to clipboard |
| `Y` | Copy any field. Opens a picker over the fingerprint, the `generatorURL`, every label, and every annotation. Type to narrow on the field name or on the start of its value, `Enter` copies. The picker cuts a long value to fit its row, and searches only the part it shows; the clipboard always gets the value in full. |
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
| `Shift+V` | Start a mark range: anchor on the cursor row, walk with `j`/`k`, then `Space` or a second `Shift+V` marks every row in between. `Esc` cancels. |
| `Shift+E` | Sort by `endsAt` |
| `Shift+S` | Sort by `startsAt` |
| `Shift+C` | Sort by creator |
| `Shift+T` | Sort by state |

### Silence detail

| Key | What |
| --- | --- |
| `y` | Toggle raw silence payload as YAML (k9s-style escape hatch); structured curated view by default. The title appends ` [raw yaml]` while raw mode is active so the two YAML views are visually distinguishable at a glance. |
| `Y` | Copy any field. Opens a picker over the ID, creator, comment, the whole matcher selector, each matcher on its own, both timestamps, and the state. Type to narrow on the field name or on the start of its value, `Enter` copies. Timestamps copy as RFC 3339. A single matcher copies as `name="value"`, the syntax `--matcher` takes. The combined `matchers` row copies one matcher per line, the syntax the silence form's matcher box reads back. |
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

### Info pane

`:info` opens the same report `a10r info` prints, for the process you
are in: resolved config dir, log path, state dir, alias count, theme,
and the configured backends.

| Key | What |
| --- | --- |
| `j` / `k` | Scroll down / up one line |
| `Ctrl+D` / `Ctrl+U` | Half-page down / up |
| `Ctrl+F` / `Ctrl+B` | Full-page down / up |
| `G` / `gg` | Jump to last / first line |
| `r` | Re-render the report |
| `Esc` | Back |

### Config pane

`:config` lists the files this start read, in the order the merge
applied them, and every warning that run produced. The source list
covers the base `a10r.yaml`, its drop-ins, `aliases.yaml`, the keys
profile, and a user skin, when each one exists.

| Key | What |
| --- | --- |
| `j` / `k` | Scroll down / up one line |
| `Ctrl+D` / `Ctrl+U` | Half-page down / up |
| `Ctrl+F` / `Ctrl+B` | Full-page down / up |
| `G` / `gg` | Jump to last / first line |
| `p` | Jump to the sources section |
| `w` | Jump to the warnings section |
| `r` | Re-render the report |
| `Esc` | Back |

### Skin switch

`:skin` with no argument opens a picker over every skin a10r can
resolve: the bundled set plus the `.yaml` files in
`<config-dir>/skins/`. The applied one is marked `(current)`. `Enter`
applies the highlighted skin and `Esc` keeps the one you have.

`:skin <name>` applies that skin without the picker. An unknown name
changes nothing and flashes ``skin "<name>" not found``, because you
named a specific skin and a silent fall back to the default would read
as a10r ignoring you. A skin that fails to compile also changes
nothing; the flash carries the error.

The change lasts for the session. It does not write `theme.name`, so
the next start reads your config file as before.

### Config reload

`:reload` re-reads your config file, your aliases file, and your keys
file. Your page stack, cursors, marks, filters, and tenant scope stay
as they are.

`:reload` applies these without a restart:

- `theme.name`
- `tui.tips` and `tui.tips_interval`
- every `poll_interval`, which restarts the pollers that changed
- `defaults.read_only`, at once everywhere — the title bar, the help
  overlay, and every page already on your stack
- per-backend `read_only`, for pages you open after the reload: it is
  enforced as a guardrail, and a page keeps the rules it was built
  with
- your aliases and your keys, as a whole-file swap

`:reload` reports success but changes nothing for `tui.notify`,
`tui.terminal_title`, and `tui.remember`. The session wired those
into the running program at startup. Restart a10r to pick them up.

`:reload` refuses the whole reload when the new file changes a
backend or the log. That covers the backend list, every field of a
backend entry except `read_only` and `poll_interval`, every `log.*`
key, and `defaults.log_format`. The session keeps the clients, the pollers, and
the log file it started with, so the flash says
`reload: backends or log changed, restart a10r` and nothing moves.

Any error stops the reload before it applies anything: a config that
no longer parses, an aliases file with a bad entry, or a keys file
that names an action a10r does not have. The flash carries the error
and the session keeps every value it had.

`:reload` is refused while a silence form is open, with the flash
`reload: close the form first`. Close the form and press it again.

`:re` is not enough to reach it, because `:receivers` starts the same
way. Type `:rel` for the reload and `:rec` for the receivers.

### Receivers / Tenant table

The lists follow the same vim motions as alerts/silences. View-specific verbs:

| View | Key | What |
| --- | --- | --- |
| Receivers | `Enter` | Drill to alerts filtered by this receiver |
| Receivers | `Shift+N` | Toggle the name sort ASC↔DESC (single sortable axis; `h`/`l` are no-ops here) |
| Tenant | `Enter` | Single-select the cursor row |
| Tenant | `Space` | Toggle the cursor row in the selection |
| Tenant | `a` | Select every tenant (with the search box empty) |

## Read-only mode

`--read-only` (or `defaults.read_only: true` in the config) hides every dangerous binding above. They stop responding and stop appearing in `?` and the right-hand hint strip — so a stray `s` or `x` during a screenshare can't fire by accident.

A per-backend `read_only: true` is per row instead. The keys stay up while any configured backend is writable, whatever your current scope, and a press aimed at a frozen backend refuses and names it. See [Guardrails](#guardrails) below — the hiding and `[guarded]` rules are the same.

## Guardrails

A `guardrails:` rule can deny a write verb on the tenants it names (see
[configuration.md](configuration.md#guardrails)). A denied verb follows
the same hiding rule as read-only: the key stops appearing in the
right-hand hint strip, and pressing it flashes a warning such as
`silence.create denied on prod-eu: use the change ticket` instead of
acting.

The difference from read-only is scope. A rule names tenants, so the
verb still works elsewhere, and `?` keeps listing it with a `[guarded]`
suffix rather than dropping the row. Read-only is checked first, so a
read-only session never mentions a rule.

A `max_bulk` rule caps how many targets one press can send to one
backend. The count is per tenant, not per run: a run that spreads ten
targets over two capped backends counts what each backend gets, never
the ten. Over the cap, the confirm modal never opens and the press
flashes a warning such as `bulk expire on prod-eu: 25 targets exceed
max_bulk 20`. Your marks stay set, so you can unmark rows and press
again.

A `confirmation: type-tenant-name` rule replaces the yes/no confirm with
a typed prompt: you retype the backend name and press Enter. A typo
keeps the prompt open and repeats what to type, and Esc cancels. A run
that touches several restricted backends asks for each one in turn, and
one Esc cancels the whole run before any write lands. A rule only
strengthens the confirmation a key already has, so a verb no rule names
keeps its usual prompt.

The silence form is a write as well, so the rules reach it too. A deny
refuses the submit with the same warning, and a rule that asks for a
confirmation asks when you submit. One write asks once: a key that
already put the question to you before it opened the form does not ask
again, and that covers the bulk form, where the key owns the whole run.
The form is where the target backend is picked, so a change of tenant
after an answer asks again for the new backend. `Ctrl+E` writes without
ever opening the form, so it asks its own question before your editor
takes the screen.

## Conventions you'll spot in the chrome

- **Title `<resource>(<scope>)[<count>]`.** The bordered panel's title shows what you're looking at. `(<scope>)` is the active tenant set; `[<count>]` is filtered/total when a filter is on, otherwise the total.
- **Cursor row** keeps the body background and brightens the foreground.
- **Marked rows** (after `Space`) tint the foreground only — different colour from the cursor so you can tell them apart at a glance.
- **Visual mode** (`Shift+V`) previews a range in that same marked style and adds a `visual` chip to the title. The rows are not marked yet: `Space` or a second `Shift+V` commits them, `Esc` cancels, and `Ctrl+\` cancels and clears every mark. A commit only ever adds, so marks you picked one by one survive. If the anchor row leaves the view — a filter change, a poll refresh — the range cancels itself and says so.
- **`TENANT` column** appears on alerts when more than one tenant is in scope. Switching to a single-tenant scope hides it.
- **Bold breadcrumbs** in the footer trace the page stack: `<alerts> <instances> <detail>` (a single-instance alert skips straight to `<detail>`). `Esc` pops one frame.
