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
  Persistent flags: `--config-dir`, `--log`, `--log-format`,
  `--debug`, `--quiet`, `--read-only`, `--tenant`,
  `--poll-interval`, `--theme`.
- Backend client: vanilla Alertmanager v2 (floor v0.28.1) read
  + write paths (alerts, silences, receivers, groups, status);
  Grafana Mimir wrapper composing prefix + tenant header on
  vanilla; multi-tenant fan-out with bounded goroutine pool.
- Config schema with env interpolation (`${VAR}`,
  `${VAR:-default}`); CLI / env / file precedence resolution;
  read-only is one-way (any-true wins).
- Structured logging via `log/slog` (json or logfmt) with
  lumberjack rotation.

### Documentation

- README with feature list, install, quickstart, keybindings.
- CONTRIBUTING with DCO, prek, TDD, commit conventions, the
  per-commit subagent review process.
- End-user docs under `docs/end-users/`: quickstart,
  configuration schema, troubleshooting recipes.
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
./a10r -c testdata/sample.yaml
#  → alerts list renders
#  → / "high" filters
#  → s on a row flashes the silence-form placeholder
#  → :silences pushes the silences page
#  → :status pushes the status pane
#  → ? opens the help overlay
#  → :q quits cleanly

# Validate read-only mode hides Dangerous bindings
./a10r --read-only -c testdata/sample.yaml
#  → ? overlay must NOT list `[s]` silence

# Snapshot release artefacts
goreleaser release --snapshot --clean
#  → archives + nfpms produced under ./dist/
```

[kac]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/
