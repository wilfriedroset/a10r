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
  is reserved.

### Smoke checklist (release-prep)

Manual walk before the v0.1.0 tag is pushed:

```sh
# Build
make build

# Spin a local Alertmanager
make am-up                    # docker run prom/alertmanager:v0.28.1

# Walk the TUI
./a10r --config-dir testdata
#  → loader picks up testdata/a10r.yaml; alerts list renders
#  → / "high" filters
#  → s on a row flashes the silence-form placeholder
#  → :silences pushes the silences page
#  → :status pushes the status pane
#  → ? opens the help overlay
#  → :q quits cleanly

# Validate read-only mode hides Dangerous bindings
./a10r --read-only --config-dir testdata
#  → ? overlay must NOT list `[s]` silence

# Snapshot release artefacts
goreleaser release --snapshot --clean
#  → archives + nfpms produced under ./dist/
```

[kac]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/
