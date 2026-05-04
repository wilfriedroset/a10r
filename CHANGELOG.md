# Changelog

All notable changes to a10r are documented in this file. The
format is based on [Keep a Changelog][kac]; the project adheres
to [Semantic Versioning][semver].

## [v0.1.0] — TBD

The first usable release. The mid-MVP scope per A1: alerts list,
alert detail, silences list, silence form, status pane,
receivers, alert groups, tenant table, plus the configuration
loader, dual-backend client (vanilla Alertmanager + Grafana
Mimir), action / keys / theme plumbing, and the bubbletea TUI
shell.

### Added

- Bubbletea v2 TUI shell with header / body / footer frame and
  page stack with push / pop / replace semantics.
- Three bundled themes: catppuccin-mocha (default), catppuccin-
  latte, gruvbox-dark; user skins shadow bundled by basename.
- Pages: alerts list (vim motions, sort walk, substring filter,
  state-filter cycle, severity column, cursor-by-fingerprint),
  alert detail (copy fingerprint, open generator URL via
  injected Clipboard / Browser interfaces), silences list,
  silence form (multi-line matchers, Prometheus operator parsing,
  RFC3339 / duration shorthand, line-precise validation), status
  pane (cluster + version + raw config viewer with anchor jumps),
  receivers (drill to alerts), alert groups (two-level tree,
  silence-by-common-labels), tenant table (numeric quick-switch,
  multi-select).
- Modal overlays: tenant picker (sahilm/fuzzy backed) and yes/no
  confirm dialog. Help overlay (`?`) auto-built from the action
  registry, hides Dangerous bindings under read-only mode.
- `:` command bar with alias resolver (`:alerts`, `:silences`,
  `:sil`, `:status`, `:q`); `/` filter prompt with snapshot /
  restore on Esc; bracketed paste in both prompts.
- Per-(backend, resource) poller with exponential backoff, ±10%
  jitter, transition-only connection-state emission, ctx-
  cancellable goroutine, stop-and-restart race-free lifecycle.
- External editor handoff via `tea.ExecProcess`, honouring
  `$A10R_EDITOR` → `$EDITOR` → platform default (`vi` /
  `notepad`).
- First-run wizard that captures the minimum-viable backend
  (URL, optional Mimir prefix, optional tenant header, auth
  type) and writes `a10r.yaml` under the resolved config dir.
- Cobra subcommands: `version`, `info`, `validate`, `completion`.
  Persistent flags: `--config` / `-c` (file path), `--config-dir`
  (directory), `--log`, `--log-format`, `--debug`, `--quiet`,
  `--read-only`, `--tenant`, `--poll-interval`, `--theme`.
- Backend client: vanilla Alertmanager v2 (floor v0.28.1) read
  + write paths (alerts, silences, receivers, groups, status);
  Grafana Mimir wrapper composing prefix + tenant header on
  vanilla; multi-tenant fan-out with bounded goroutine pool.
- Config schema with env interpolation (`${VAR}`,
  `${VAR:-default}`); CLI / env / file precedence resolution;
  read-only is one-way (any-true wins).
- Structured logging via `log/slog` (json or logfmt) with
  lumberjack rotation.

### Polish (post-scaffold UX iterations)

- k9s-style top panel: four columns laid out as info / tenant
  numeric quick-switch / per-page hints / ASCII A10r logo, with
  per-row gap-elision so narrow terminals degrade cleanly.
- Bordered body panel with the title `<resource>(<scope>)[<count>]`
  on the top edge, mirroring k9s. Subtitle line surfaces filter
  / sort / mark state when active and stays empty otherwise.
- Cursor row keeps the body background and brightens the
  foreground; marked rows tint foreground only with a different
  hue so the two affordances are visually distinct.
- Column header row is foreground-only (no header stripe) with
  uppercase labels and a sort arrow `↑`/`↓` on the active column.
  Sort shortcuts toggle ASC↔DESC on repeat press; switching
  columns resets to that column's natural default.
- Numeric tenant quick-switch (`<0>` all, `<1>`-`<9>` per backend
  in `backends:` order) wired at LayerGlobal so it works from
  every page. Alerts page rescopes its `byTenant` snapshots, the
  TENANT column appears iff scope=="all" and ≥2 backends carry
  data, and the title's `[N]` count reflects the active scope.
- Tenant page mirrors the global scope visually: `●` glyph on
  in-scope rows plus a foreground tint, so the user can spot
  the active fan-out without leaving the page.
- Status page rescopes its title on `app.ScopeChangedMsg` (per-
  backend status fan-out is a v0.2 concern; the body still
  reflects the latest poll).
- Help overlay rebuilt as a k9s-style four-column layout
  (RESOURCE / GENERAL / NAVIGATION / HOTKEYS) inside the App's
  outer panel border. RESOURCE auto-merges the tenant numeric
  list with the active page's verbs; HOTKEYS auto-collects
  page-bound `Shift+*` sort shortcuts. Read-only mode filters
  Dangerous out of both halves.
- Per-page polling fan-out: every entry in `cfg.Backends` gets
  its own poller emitting `poll.DataMsg` tagged with its tenant
  name. List pages can union the snapshots into a `byTenant`
  map (alerts uses this today; the silences/receivers/groups
  fan-out lands in v0.2 alongside their own pollers).
- Modal interface gains a `Title()` method so the App's outer
  panel labels the border (`Help`, `tenant`, `confirm`, …)
  instead of a generic `modal` placeholder.
- `<?> help` is no longer advertised on per-page hint strips;
  `?` is global only.
- Command (`:`) and filter (`/`) prompts moved from a bottom-of-
  screen footer strip to a bordered panel directly above the
  body — same chrome shape as k9s. Mode prefixes use the k9s
  emojis: 🐶 for command mode, 🐩 for filter mode. The body
  title carries a live `</value>` segment while the filter
  prompt is open so the active filter is visible without
  leaving the body in your peripheral vision.
- Filter prompt is now live: every keystroke (and paste, and
  backspace, and Ctrl+U) refilters the alerts list as you type.
  Pressing `/` while a filter is active clears the filter so
  typing rebuilds it from scratch — the pre-prompt value is
  snapshotted, so Esc still rolls back. Enter on an empty
  prompt clears the filter; Enter on a typed value commits it.
- `Ctrl+F` / `Ctrl+B` round out the vim viewport-motion set on
  every scrollable page (alerts, silences, alert detail, status,
  tenant config) — full page down / up siblings of `Ctrl+D` /
  `Ctrl+U`. All four steps are now viewport-aware: each page
  snapshots its rendered body height so `Ctrl+D` / `Ctrl+U` walk
  half the actual window and `Ctrl+F` / `Ctrl+B` walk a window
  minus two lines (vim's CTRL-F context overlap), instead of the
  prior hard-coded 10 / 20.

### Silence write surface

- `s` on the alerts list, alert detail, and groups pages now
  pushes the silence form prefilled with matchers from the
  source resource — the alert's labels minus the synthetic
  `__name__` for single-row silences, the group's common-label
  intersection for the groups view. Tenant follows the cursor
  row's tag so a multi-backend run hits the right backend
  without an extra prompt.
- `e` on the silences list pushes the silence form in edit mode
  prefilled from the cursor row (matchers / comment / endsAt /
  EditID); submit calls `UpdateSilence`. The form's
  `SubmittedMsg.Updated` flag picks the parent flash wording so
  edits read "silence updated: <id>", not "created".
- `x` on the silences list opens a confirm dialog with default-No
  (a stray Enter never destroys data) and calls `ExpireSilence`
  on Yes. `Ctrl+X` does the same in bulk over `Space`-marked
  rows; marks survive sort / filter changes by tracking silence
  ID. Marked rows render a `✓` glyph and a foreground tint so
  the bulk-confirm question always has a row-level reference.
  Pending {id, tenant} pairs are captured at modal-open time so
  a poll-tick reordering between Open and Yes never reroutes
  the expire to the wrong backend.
- `Ctrl+E` on the silences list round-trips the cursor silence
  as YAML through `$EDITOR` (or `$A10R_EDITOR`). Saving applies
  the edit via `UpdateSilence`; aborting without saving is a
  silent no-op. Validation matches what the API requires (≥1
  matcher, ends after starts, non-empty creator + comment) so a
  malformed edit flashes a precise error instead of round-
  tripping a 400.

### Bulk silence and bulk expire (k9s same-key-different-N)

- `s` on the alerts list now silences every marked alert when one
  or more rows are marked, falling back to the cursor-row form
  when no marks are active. The bulk path opens the silence form
  once for the metadata (comment, starts/ends, creator), then
  fans out one `CreateSilence` per marked alert with that alert's
  labels (minus the synthetic `__name__`) as matchers. Multi-
  tenant marks fan out per-tenant: tenants run in parallel, each
  with a bounded worker pool capped by `defaults.bulk_concurrency`
  (default 4). Failed silences keep their marks so the next `s`
  retries only the unfinished work.
- `x` on the silences list now expires every marked silence when
  one or more rows are marked, falling back to the cursor-row
  confirm when no marks are active. Multi-tenant fanout, idempotent
  on the AM side. Same `defaults.bulk_concurrency` knob.
- `Ctrl+S` (alerts) and `Ctrl+X` (silences) are removed; the
  single-binding rule is the whole point.
- `Ctrl+\` clears every mark on the focused page in one keystroke.
- `defaults.bulk_concurrency` (default 4) tunes the per-tenant
  worker pool for both bulk silence and bulk expire. `1` collapses
  to fully sequential per tenant; tenants always run in parallel.

### Silences page polling UX

- Cold-start "loading" empty state on the silences page —
  bubbles `Points` spinner plus "loading silences from <scope>…"
  so a fresh open reads as "we're asking" rather than the
  ambiguous "no silences (yet)" answer. Empty-state pane now
  uses the terminal default background to match the regular
  table view's framing.
- New `Page.Footer()` interface method; rendered centred in the
  bordered body's bottom edge via a new `panel.RenderBody`
  parameter, k9s-style symmetry with `Title` in the top edge.
  Silences page uses it for "next refresh 26s" — drawn from a
  new `poll.DataMsg.NextAt` field the loop publishes alongside
  the payload, no parallel ticker. "refreshing…" overrides the
  static label between an `r` press and the next DataMsg.
- `r` on the silences page emits a typed
  `app.RefreshRequestedMsg{Resource, Scope}`; the wiring layer
  routes it to a per-(resource, tenant) `pollerRegistry`, which
  calls `Refresh()` on each matching `*poll.Poller`. Refresh
  coalesces (buffered slot) and leaves the failure backoff
  intact so a manual nudge against a flaky upstream doesn't
  pretend the previous attempts didn't happen.
- Expired silences render with the foreground-only Dimmed style,
  mirroring the suppressed-alert treatment on the alerts page —
  cursor / marked rows still win precedence.

### UX polish, batch 1

- Backend HTTP requests now carry an RFC 9110 User-Agent built
  from the cmd build vars — `a10r/<version>` for plain releases,
  `a10r/<version> (<commit>)` when a non-default commit is
  available — so backend operators can tell a10r traffic apart
  from any other Go HTTP client.
- Alerts list SEVERITY cell now wears the matching theme
  Severity.{Critical,Warning,Info,Unknown} foreground; cursor /
  marked / suppressed rows preserve the row-level style by
  skipping the per-cell colour.
- Top panel caps at logo height: tenants and per-page hints lay
  out as up-to-3-column k9s-style grids (column-major fill),
  items past the budget silently clip. The labelled info column
  inherits the same height clip.
- Spinner cold-start and manual `r` refresh on the alerts and
  groups pages, mirroring the silences page: title flips to
  "<spinner> loading…" while a load is in flight, footer reads
  "refreshing…" / "next refresh Ns", `r` emits
  `app.RefreshRequestedMsg`. The alerts page also picks up the
  silences-page comma-joined scope predicate.
- Groups page colours `k=v` label pairs with `theme.YAML.Key /
  .Punct / .Value`, applied to leaf-row alertname / state too.
  Adds a leading TENANT column on group header rows when scope
  spans more than one in-scope backend.
- Tenant table reshaped as a read-only inspector with NAME / URL
  / VERSION columns; Enter drills into a new tenant-config page
  that surfaces the redacted `a10r.yaml` entry alongside the
  live Alertmanager `config.original`. Backend versions are
  fetched once at startup with a per-call timeout; failures
  render as `—`. Auth secrets (basic password, bearer token,
  header value) are masked to `***` before they reach the
  screen.
- App-global `t` toggles between relative ("5m ago") and
  absolute ISO local ("2026-05-01 13:45:00") timestamps. The
  alerts list, silences list, and alert-detail page all observe
  the announcement and re-render their AGE / ENDS / STARTS
  columns, widened to 20 cols in absolute mode. HeaderContent
  surfaces "time:absolute" while the non-default mode is active.
- Alerts state-filter cycle moved from `t` to `Shift+F` so the
  app-global time-format toggle can claim `t` cleanly.

### Schema: Prometheus `remote_write` parity (breaking)

Backend entries in `a10r.yaml` now use the same shape as Prometheus's
[`remote_write`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write)
block — paste a `remote_write` entry under `backends:`, adjust the
`url:` path to your Alertmanager v2 root, and you are done. Detail
in `docs/design/prometheus-remote-write-parity.md` (open-questions
F4); migration notes for users coming from earlier development
builds:

- Auth blocks moved from a nested `auth: { type, basic|bearer|header }`
  envelope to flat siblings of `url:` —
  `basic_auth: {username, password}`, `authorization: {type,
  credentials}`, `bearer_token: <token>`. At most one of the three
  is allowed per backend.
- The single-header auth shape (`auth.header.{name,value}`) is
  superseded by a free-form `headers: { Name: value, … }` map at
  the backend level. Reserved keys (`Authorization`, `Host`,
  `Content-Type`, `Content-Length`, `Content-Encoding`) are
  rejected at load time.
- New optional `tls_config:` block accepts inline-only `ca:`,
  `server_name:`, `insecure_skip_verify:`, `min_version:`,
  `max_version:`. `cert:` / `key:` are reserved for the F2 mTLS
  work and currently rejected with a pointer to that question.
- New `proxy_url:`, `no_proxy:`, `proxy_from_environment:` knobs
  match Prometheus's `ProxyConfig` semantics.
- New `remote_timeout:` per backend; replaces the previously
  hard-coded 30 s in `vanilla.Client`.
- `tenant_header:` / `tenant:` are retained as a10r-specific YAML
  sugar that materialises into a single `headers:` entry at
  construction time; setting both `tenant:` and a colliding
  `headers:` entry is a load-time error.
- Examples migrated: `examples/two-tenants-basic-auth.yaml` uses
  the flat shape; new `examples/prometheus-paste.yaml` shows
  every supported field with PEM, proxy, and headers blocks.

`*_file` and `*_ref` keys are deliberately not supported (per F1 —
credentials live in env vars referenced via `${VAR}`); pasted
Prometheus configs that use file-based secrets surface a
strict-mode error naming the offending key.

### Documentation

- README with feature list, install, quickstart, keybindings.
- CONTRIBUTING with DCO, prek, TDD, commit conventions, the
  per-commit subagent review process.
- End-user docs under `docs/end-users/`: quickstart, per-view
  keybindings cheat-sheet, configuration schema, troubleshooting
  recipes.
- Design docs under `docs/design/`: open-questions resolutions
  (sections A–P), keybindings catalogue, theming spec,
  backend API audit, k9s look-and-feel notes.

### Caveats

- Mimir config-API editor and ring inspector are deferred —
  capability flags are plumbed but the corresponding UI lands
  in a future release.
- Silence write API and the silence form's submit path are wired
  but exercise only in single-backend setups; multi-tenant write
  flows arrive in v0.2.
- mTLS and SigV4 auth are not yet implemented; the schema slot
  is reserved (see open-questions F2 / F3, and the
  Prometheus-parity doc for OAuth2 / Azure AD / Google IAM, which
  are deferred for the same reason).

### Smoke checklist (release-prep)

Manual walk before the v0.1.0 tag is pushed:

```sh
# Build
make build

# Spin a local Alertmanager
make am-up                    # docker run prom/alertmanager:v0.28.1

# Backend smoke harness against the local Alertmanager
make smoke
#  → status / receivers / alerts / silences / alert_groups all
#    queried; silence created → fetched (active) → expired

# Walk the TUI
./a10r -c examples/local-am.yaml
#  → loader picks up the local backend; alerts list renders
#  → / "high" filters
#  → s on a row flashes the silence-form placeholder
#  → :silences pushes the silences page
#  → :status pushes the status pane
#  → ? opens the help overlay
#  → :q quits cleanly

# Validate read-only mode hides Dangerous bindings
./a10r --read-only -c examples/local-am.yaml
#  → ? overlay must NOT list `[s]` silence

# Snapshot release artefacts
goreleaser release --snapshot --clean
#  → archives + nfpms produced under ./dist/ — Linux amd64/arm64/
#    armv7, FreeBSD amd64/arm64, Windows amd64/arm64, plus a single
#    Darwin universal tarball (amd64+arm64 merged via goreleaser's
#    universal_binaries) and deb/rpm/apk for linux amd64/arm64/arm

# Tear down
make am-down
```

[kac]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/
