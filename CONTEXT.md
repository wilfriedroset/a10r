# a10r

Terminal UI for Alertmanager / Mimir: alert triage, silence authoring,
multi-tenant scopes.

## Language

### Alert aggregation

**Alert**:
The named aggregate the alerts list rows on — every **alert
instance** sharing the same `(tenant, alertname)`. One row per
distinct alertname per tenant; the COUNT column carries the
instance tally, AGE the oldest instance `StartsAt`, SEVERITY the
max (worst) instance severity, STATE the **state breakdown**. A
TUI-layer concept (no backend type) synthesised by rolling up
`backend.Alert` values by alertname. Grouping is on the `alertname`
label alone, so two distinct upstream rules emitting the same
alertname within one tenant merge into one Alert — over-merge only,
never over-split (a single rule sets one alertname), and AM exposes
no rule identity to do better.
_Avoid_: rule (AM has no rule concept; we group on the alertname
label, not a rule).

**Alert instance**:
One firing series — a single `backend.Alert` (one fingerprint, one
host/target). What the wire layer calls an Alert; the unit the
group-detail instance list rows on and the unit a single-instance
silence matches. Carries its own state, labels, annotations,
generatorURL, and `StartsAt`.
_Avoid_: alert (unqualified "alert" is now the **aggregate**),
series (Prometheus term), sample.

**State breakdown**:
The **Alert** row's STATE column — a per-state tally of the group's
instances across AM's `active` / `suppressed` / `unprocessed`
buckets, non-zero buckets only, fixed order active → suppressed →
unprocessed; the three counts sum to COUNT. Two render modes toggled
app-globally, parallel to the **absolute/relative time** toggle:
**full** (`9 active · 3 suppressed`, the default) and **compact**
(`9ac 3su` — count + two-letter state abbreviation, chosen to avoid
colliding visually with the `s` / `S` silence verbs and to stay
legible without color). Suppression sub-reasons (silenced / inhibited / muted) stay
off the list and surface only on the instance view.
_Avoid_: status (AM wire field name), firing / pending (Grafana
ruler vocabulary — AM has no pending state).

### Alert drill-down

**Group detail**:
The L2 list page reached by Enter on a multi-instance **alert** at
L1. Rows on **alert instances** (one per fingerprint); columns
`SEVERITY <specific-labels> STATE AGE` — the L1 order minus
TENANT/COUNT, with the flex column holding each instance's
instance-specific labels (those outside the common set) — the
`instance` label pinned first so truncation never eats the primary
identifier, the rest ellipsized to one line. Live: polls the alerts
feed scoped to its `(tenant, alertname)`. A one-line **common
labels** strip renders above the table by default (the shared
cluster / job / datacenter is usually the first triage question);
a toggle collapses it for row density. A plain list page with that
optional strip, not a detail surface.
_Avoid_: alert detail (that name now means L3 / **instance
detail**), expanded row (L2 pushes a page, it does not inline-expand
the way Grafana's web table does).

**Instance detail**:
The L3 page — the existing alert-detail surface
(`internal/tui/page/alert`): one fully-expanded **alert instance**
with full labels, annotations, generatorURL, and suppression block.
Reached by Enter on an L2 instance row, or directly from L1 when an
alert has COUNT==1 (the single-instance shortcut skips L2).
_Avoid_: group detail (L2).

### Silence scope

**Silence-all**:
Silence every instance of an **alert**, prefilled with a single
`alertname=<name>` equality matcher — the alert's identity —
editable before submit. Reached by `s` on an L1 alert row (no
marks); from L2 via `Esc` to L1 (the cursor is already on the
aggregate). A single-cursor silence-all of a COUNT>1 alert is gated
by the confirm modal — blast radius, not mark count, decides the
gate. The form carries a **scope note** stating the
real scope and, when the source view was filtered, that the active
filter is NOT applied.
_Avoid_: bulk silence (that is the marks fan-out — a different
verb).

**Silence-one**:
Silence a single **alert instance**, prefilled with that instance's
full non-private label set (`MatchersFromLabels`, drops `__name__`).
Reached by `s` on an L2 instance row (no marks) or on L3. Bulk
silence-one (marks fan-out) at L2 warns when the marked count is
large — N full-label silences are a very different artefact from one
`alertname=X` **silence-all** for nearly the same intent.
_Avoid_: silence-all.

**Scope note**:
The caller-supplied banner above the silence form's matchers,
populated for **silence-all** to state the true scope and flag that
any active list filter is not applied. Empty for **silence-one**.
_Avoid_: flash (the note is persistent in the form, not a transient
footer flash).

### Time rendering

**Relative time**:
The distance between `now` and a timestamp, rendered as `X ago` when in
the past or `in X` when in the future. Single-unit (s/m/h/d). Used in
compact table columns (alert age, silence STARTS / ENDS).
_Avoid_: age (past-only), remaining (mixed-unit prose), ETA.

**Absolute time**:
The same timestamp rendered as `YYYY-MM-DD HH:MM:SS` in local zone,
toggled app-globally with `t`.

**Remaining**:
The mixed-unit forward-looking duration (`2h13m`, `4d`) used in
narrative fields such as the alert-detail `expires in …` line. Not used
in tables. The vocabulary is strictly forward-looking — a non-positive
duration is out-of-domain for Remaining, so the renderer returns the
empty string and the caller owns any past-case label (the alert detail
page renders its own `expired`).
_Avoid_: countdown, ETA.

### Silence lifecycle

**Active silence**:
A silence whose window covers `now` (StartsAt ≤ now < EndsAt). ENDS is
in the future → rendered as `in X`.

**Pending silence**:
A silence scheduled for a future window (now < StartsAt). STARTS is in
the future → rendered as `in X`.

**Expired silence**:
A silence whose window has elapsed (EndsAt ≤ now). ENDS is in the past
→ rendered as `X ago`.

**Recreate**:
Derive a new silence from an existing one: matchers and comment are
copied; the window resets (starts at now) and the expiry must be
restated, never assumed — re-applying a stale window silently is the
failure mode the verb exists to prevent. Creator resets to the acting
user. The source silence is untouched and may be in any lifecycle
state. Distinct from updating, which mutates a silence in place and
is only possible while it is **active** or **pending**.
_Avoid_: clone / copy (imply the window is carried over too), re-arm,
restore, edit (in-place mutation).

**Restricted silences view**:
The silences list page pushed from the alert-detail `S` binding,
scoped to the alert's silenced-by IDs (frozen at push time). Same
chrome, sort, filter, marks, and write verbs as the regular
silences page; the title substitutes the alert name for the scope
label (`silences(<alertname>)`) so the operator knows which alert
they drilled in from. `n` (new) prefills the silence form's
matchers from the alert's labels, matching alert-detail `s` rather
than the global silences page's blank `n`. Replaces the retired
alert-page silence picker (modal) so the multi-silence path lands
in the same shape the operator already knows.
_Avoid_: silence picker (retired modal kind), suppression view
(suppression is the alert-detail body section, not a separate
page), silenced-by modal (no longer a modal).

### Label matcher

**Label matcher**:
A `name<op>value` predicate over a single label, in Prometheus syntax.
The four operators are `=` (literal equal), `!=` (literal not-equal),
`=~` (regex equal), `!~` (regex not-equal). Surfaces: the `--matcher`
flag, the silence form textarea (one per line), and the matcher
slice carried on every `backend.Silence`. Parsed and rendered by
`internal/matcher`. The leftmost-operator-wins rule with two-char
operators winning a position tie is load-bearing for round-tripping:
`foo=a!=b` splits on the first `=` (value `a!=b`), and `foo=~bar`
parses as regex match rather than literal-equal of `~bar`.
_Avoid_: filter (the user-facing `--matcher` flag is a filter, but
the underlying value is a matcher), selector (PromQL term), label
predicate.

### Duration shorthand

**Duration shorthand**:
The free-text grammar accepted by the silence form's `Ends` field
alongside RFC3339. Grammar: one or more `<float><unit>` terms with
units `s`, `m`, `h`, `d`, `w` (`m` is **minute**, never month;
capital `M`/`W`/`Y` are rejected with a tailored error). Terms must
appear largest-first (`7d2h`, not `2h7d`); each unit appears at
most once. AM's silence form accepts the same units but isolates
the grammar in a separate `duration` field bidirectionally linked
to a RFC3339 `endsAt`; a10r overloads a single `Ends` field
instead, trading the live cross-field recompute for fewer focus
stops. The input grammar accepts `w` even though the **relative
time** and `Duration` output ladders cap at `d` — the asymmetry is
deliberate: operators type `1w` for a week and read `7d` for the
same span. Parsed by `internal/tui/timerender.Parse`. A no-number
alpha-run is classified by length: ≤2 letters reads as an attempted
unit (`dx`, `xh` → `expected number before unit %q`); ≥3 letters
reads as English garbage (`hello` → `not a duration`). The cutoff
is load-bearing for the unified error: every unit symbol is a
single letter, so a longer run cannot be a unit attempt.
_Avoid_: duration (unqualified — collides with stdlib
`time.Duration` and with the `timerender.Duration` renderer), ttl,
expiry, length.

### Overlays

**Overlay**:
A UI surface that takes precedence over the page stack and captures
keyboard input while visible. Two kinds: **modal overlay** and
**help overlay**. Only one is open at any moment.
_Avoid_: popup, dialog (Western GUI vocabulary), panel (unqualified
"panel" is the **top panel** app-chrome surface; say "overlay").

**Modal overlay**:
An async-result overlay — the user makes a decision and the result
returns as a typed message (`ConfirmResultMsg`,
`PickerResultMsg`, ...). Concrete kinds today: tenant picker,
yes/no confirm. All satisfy `modal.Modal`. The alert-page silence
picker was retired in favour of the **restricted silences view**
(see Silence lifecycle).
_Avoid_: modal dialog, prompt (prompts live in the footer command bar).

**Help overlay**:
A viewer overlay — renders the `?` keybindings catalogue for as
long as the user looks at it. No decision pending; any non-scroll
key dismisses. Sole kind: `help.Help`, which does not satisfy
`modal.Modal` and lives in its own routing slot.
_Avoid_: help modal (the rejection is the point of ADR 0020),
keybindings panel (the **hint grid** is the panel zone; this
overlay is `Help`).

### Top panel

**Top panel**:
The app-level k9s-style strip rendered above every page. Three
zones: the **tenant shortcut grid** (left), the **hint grid**
(middle), and the ASCII logo (right). Stateless: rebuilt every
frame from the app's tenant list and the top page's `Bindings()`.
Distinct from the **header** strip (which is a single-line
three-zone surface used by other pages) and from list-page
**chrome** (border-frame, page-scoped). Owned by
`internal/tui/panel/panel.go`'s `RenderTop`.
_Avoid_: header (different code path, different geometry),
namespace bar (k9s vocabulary; a10r says tenants).

**Hint grid**:
The middle zone of the **top panel** — a column-major grid of
`<key> Description` cells from the active page's `Bindings()`,
capped at three columns and at logo height (five rows). Chips
render k9s-style lowercase via `help.ChipText` (`<shift-s>`,
`<ctrl-e>`, `<enter>`, `</>`); a bare uppercase single-letter
binding (alert detail's `S`) expands to `<shift-<letter>>` so
it stays distinct from its lowercase sibling (`s` silence vs
`S` open silences) in the same hint grid. See ADR 0037. The
renderer reflows under width pressure per ADR 0036: drop logo
→ step hint cols down 3 → 2 → 1 → iteratively drop trailing
hints once 1-col-at-cellW still won't fit, recomputing `cellW`
after each drop. Pages register hints most-important-first so
the drop-from-end loses the least-load-bearing verbs; the full
catalogue stays one `?` away in the **help overlay**.
_Avoid_: menu (k9s name), key bar (footer vocabulary), hint
strip (the hint strip is the single-line right-zone of the
**header** surface, not the grid), `<Shift+S>` / `<Enter>`
(chips are k9s-style lowercase per ADR 0037).

**Tenant shortcut grid**:
The left zone of the **top panel** — a column-major grid of
`<N> name` cells, with the synthetic `<0> all` followed by
`<1>..<9>` for the first nine configured tenants. Renders at
its natural cols width; the **hint grid** absorbs whatever
budget is left over (tenants-first priority per ADR 0036)
because losing a tenant shortcut silently would corrupt the
operator's multi-tenant model.
_Avoid_: namespace shortcuts (k9s), tenant picker (the picker
is the modal kind in **modal overlay**), scope picker.

### Backend health

**Backend health**:
The per-tenant transport state a list page holds for rendering the
**error band**; carries (state, detail, failures, **next attempt**).
State is one of *connected* / *degraded* / *unreachable*. An entry
exists only while a tenant is not connected; cleared on recovery.
_Avoid_: backend status (the wire-format message that mutates this
value), connection state (header chrome only).

**Next attempt**:
The failure-mode tick clock rendered in the **error band** using
single-unit **relative time** — `retrying in 5s`, `retrying in 1m`.
When the clock is past-due (a tick is in flight), the band suffix
becomes `retrying now`.
_Avoid_: retry deadline, backoff (poller implementation detail).

**Error band**:
The one-line surface above the table that narrates per-tenant
**backend health** for tenants in scope. Empty when every in-scope
tenant is **connected**. Multi-offender layouts collapse to a count
plus the alphabetically first offender's detail and **next attempt**.
_Avoid_: status line, error banner.

### List-page chrome

**Chrome**:
The border-frame surfaces of a list page — title, header, footer,
**error band**, and empty-pane wrap. Distinct from the data rows the
table renders. Chrome stays on the terminal default background
(fg-only renderers) so the unstyled frame doesn't break the populated
table's seam.
_Avoid_: frame (informal name for the lipgloss wrap, not the surface
set), border (the visual line, not what it bounds).

**Loading affordance**:
The spinner-led title prefix shown during a loading window —
`⣷ loading alerts…`. Active when no in-scope tenant has produced a
DataMsg yet (cold start) or while a manual `r` refresh is in flight.
Two of five list pages render it (alerts/silences);
receivers/tenant/status have no spinner.
_Avoid_: spinner title (the spinner is one part), loading state
(too generic).

**Refresh countdown**:
The bottom-border footer that surfaces refresh state for polled list
pages. Five branches: `""` (pre-poll), `"next refresh Ns"` (single-
unit **relative time** with `next refresh` as the prefix),
`"refreshing…"` (manual `r` in flight), `"WATCH OFF"` (paused),
`"WATCH OFF · refreshing…"` (paused with a pausedRefresh in flight).
Same two pages as the **loading affordance** render the full
cycle; receivers shows only the WATCH OFF branch; tenant/status omit
the footer entirely.
_Avoid_: refresh footer (surface name, not content), poll status
(too generic), watch indicator (one branch only).

### Theming

**Skin**:
A k9s-format YAML file declaring colors for the TUI's roles,
consumed drop-in (any community k9s skin loads without conversion).
The unit of theme distribution.
_Avoid_: theme (generic; "skin" is the operative term aligned with
k9s upstream), colorscheme, palette (palette is a layer *inside* a
skin, not the skin itself).

**Bundled skin**:
A skin embedded in the binary via `embed.FS`, available as
`--theme <name>` on a fresh install. Two provenance sub-classes:
**synced** (declared in `SOURCES.yaml.sources[]`, copied verbatim
by `make skins-sync`) and **authored** (declared in
`SOURCES.yaml.authored[]`, hand-edited in-tree).
_Avoid_: built-in theme, default skin (default is a *role* one
bundled skin fills, not a class).

**User skin**:
A skin file under `<config-dir>/a10r/skins/<name>.yaml` provided
by the end user. Resolved ahead of bundled skins in the loader;
shadow-warns if its name matches a bundled skin.
_Avoid_: custom theme, override skin.

## Relationships

- An **Alert** rolls up one or more **alert instances** sharing the
  same `(tenant, alertname)`. COUNT is the instance tally, AGE the
  oldest instance `StartsAt`, SEVERITY the max instance severity,
  STATE the **state breakdown**.
- The substring (`/`) and state (`Shift+F`) filters narrow **alert
  instances** first; **Alerts** are then rebuilt from the survivors
  and an Alert with no surviving instance drops from the page, so
  COUNT / STATE / AGE always describe the post-filter reality.
- Drill-down ladder: L1 alerts list → (Enter) → L2 **group detail**
  → (Enter) → L3 **instance detail**; Esc pops one level. Enter on a
  COUNT==1 alert at L1 skips L2 and lands on L3 directly — such rows
  carry a trailing `→` marker so the drill destination is
  predictable from the row, and L2 / L3 share a consistent keymap so
  the skip is benign. L1 cursor / marks are keyed by group
  `(tenant, alertname)`; L2 by instance fingerprint; marks are
  per-page and do not cross levels.
- `s` silences the unit under the cursor — an L1 **alert** →
  **silence-all** (gated by the confirm modal when COUNT>1), an
  L2 / L3 **alert instance** → **silence-one**; marks escalate `s`
  to a bulk fan-out (one silence per marked unit, ≥2 marks gated by
  the confirm modal). `S` = open silences consistently at every
  level — the **restricted silences view** scoped to the silences
  suppressing the cursor instance (L3) or any instance of the alert
  (L2, the union of silenced-by IDs). **Silence-all** has no L2 key;
  it is reached by `Esc` to L1.
- A **silence** is exactly one of **active**, **pending**, **expired**
  at any given moment (backend-decided, client renders what it sees).
- **Relative time** and **absolute time** are two render modes for the
  same underlying timestamp, swapped by the global `t` key.
- **Relative time** (compact, single-unit) and **remaining**
  (mixed-unit, prose) are two distinct rendering shapes — the former
  for table columns, the latter for narrative fields.
- **Backend health** entries exist per tenant only while not
  **connected**; the **error band** renders only in-scope entries.
- **Next attempt** reuses the single-unit **relative time** vocabulary
  with `retrying in` as the prefix instead of bare `in`.
- The **top panel** sits above every list page's **chrome** and
  reflows under width pressure in this order: drop the ASCII logo
  → step **hint grid** cols down 3 → 2 → 1 → drop trailing hints
  from the end. The **tenant shortcut grid** keeps its natural
  cols width; the **hint grid** absorbs what's left. Panel height
  is capped at logo height even when the logo itself is dropped.
- A list page's **chrome** comprises title (with optional **loading
  affordance** prefix), header, footer (with optional **refresh
  countdown**), **error band**, and the empty-pane wrap.
- **Loading affordance** and **refresh countdown** are co-present — a
  page renders both or neither (alerts/silences render both;
  receivers/tenant/status render neither).
- **Refresh countdown**'s `"next refresh Ns"` branch reuses the
  single-unit **relative time** vocabulary with `next refresh` as the
  prefix instead of bare `in`.
- A **modal overlay** takes input precedence over a **help overlay**;
  the two never render simultaneously. `?` is shadowed while a modal
  is open so a pending decision is not dismissed off-screen.
- A **skin** is either a **bundled skin** or a **user skin**; a
  **bundled skin** is either **synced** (mirrored from upstream) or
  **authored** (in-tree). The loader resolves user skins ahead of
  bundled.
- **Duration shorthand** and the **Remaining** / **Relative time**
  output ladders share `internal/tui/timerender` and the `s/m/h/d`
  vocabulary; the input grammar adds `w` at the top, the output
  ladders cap at `d`.

## Example dialogue

> **Dev:** "The ENDS column always shows `now` for active silences — can
> we get `in 2h` like expired ones show `2h ago`?"
> **Maintainer:** "Yes — that's relative time. The helper today is
> past-only; we'll extend it to handle future deltas symmetrically, so
> active silences render `in X` and pending silences' STARTS does too."
