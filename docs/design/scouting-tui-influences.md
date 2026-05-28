# Scouting: TUI/CLI influences for a10r

## Scope and method

We reviewed seven Go projects for UX/UI patterns worth porting into
a10r. The scope is intentionally broader than pure TUIs: CLI
ergonomics (output formatting, `doctor` command, init wizard, exit
codes) matter for the surface around a10r's TUI as much as the in-TUI
behaviour.

Tools scouted:

- `derailed/k9s` — the *explicit reference* a10r mirrors in look-and-feel
  (`docs/design/k9s-look-and-feel.md`); tview/tcell-based, plugin and
  alias YAML surfaces, custom column views
- `muesli/duf` — styled CLI with table-renderer, theming, terminal
  capability detection
- `wtfutil/wtf` — TUI dashboard built on tview/tcell with widget/grid
  composition
- `yorukot/superfile` — modern bubbletea file manager with sidebar,
  command bar, modal stack, theme files
- `janosmiko/lfk` — modern bubbletea Kubernetes navigator, k9s-spirit
  successor on a10r's exact stack; multi-tab, bookmarks, search-mode
  autodetect, runtime dark/light, ghostty-theme codegen, in-TUI
  Alertmanager monitoring overlay
- `twpayne/chezmoi` — polished cobra CLI with `doctor`, `init`, config
  layering, templating
- `trufflesecurity/trufflehog` — long-running multi-source scanner CLI
  with output strategy, progress, redacted logging

Each candidate below is cross-checked against a10r's current state
(`internal/tui/*`, `internal/config`, `internal/backend`, `cmd/*`,
`docs/design/*`) so we only flag genuinely new ideas. Items already
present in a10r are not listed as candidates; they appear in the
per-tool sections only as "skip — already implemented" notes when
relevant.

## Alignment check against k9s (the explicit reference)

Before the candidate list: a10r mirrors k9s on the patterns that
matter most. Confirmed alignment, with both file-line refs:

- **Three-zone frame** (header / page stack / footer): k9s
  `internal/view/app.go:45-59` ↔ a10r `internal/tui/app/app.go:68-122`.
- **Page stack with Esc/breadcrumbs**: k9s
  `internal/view/page_stack.go:14-57` ↔ a10r
  `internal/tui/app/app.go:80-83`.
- **Command bar with prefix-resolved aliases**: k9s
  `internal/view/command.go:34-68` (`dao.Alias`) ↔ a10r
  `internal/tui/cmdbar/cmdbar.go:48-82` + prefix matching at 96-127.
- **Help overlay sourced from action registry**: k9s
  `internal/ui/action.go` (`KeyActions.Hints()`) ↔ a10r
  `internal/tui/help/help.go` (registry from `internal/tui/action`).
- **Read-only mode strips dangerous bindings**: k9s
  `internal/ui/action.go:159-169` (`ClearDanger`) ↔ a10r
  `internal/tui/action/action.go:48-50` + dispatcher filter.
- **Theme files (skins) with user override**: k9s
  `internal/config/styles.go` + `skins/*.yaml` ↔ a10r
  `internal/tui/theme/loader.go` + `<config-dir>/skins/`.
- **Numeric quick-switch + picker for context/tenant**: k9s context
  switcher ↔ a10r tenant picker (`internal/tui/app/app.go:160-181`).
- **Vim motions + chord support**: a10r's explicit chord timeout in
  `internal/tui/keys/dispatch.go:26,59-65` is more deterministic than
  k9s's tcell-implicit timing; this is a *gain*, not a divergence.

Intentional divergences from k9s — sound, do not "fix":

- **bubbletea/lipgloss vs tview/tcell.** a10r's choice composes
  better with the action-registry / dispatcher split and is more
  testable. k9s predates bubbletea's maturity.
- **Action registry is metadata-driven** (a10r) vs handler-coupled
  (k9s `internal/ui/action.go:32-46`). a10r's split lets help and
  read-only filtering be exercised without invoking handlers.
- **Filter syntax specialised for the alerting domain.** k9s carries
  a fuzzy + regex + label-selector parser
  (`internal/model1/table_data.go:143-219`,
  `internal/view/cmd/interpreter.go`) for 30+ heterogeneous resource
  types; a10r uses Prometheus matchers for silences and substring for
  alerts, and is *adopting* k9s's label-selector pattern via F2.
- **Status page is flat, not Xray-style hierarchical.** Alertmanager
  has no natural tree (alerts→groups is N-to-M). The page-stack
  drill from receivers → alerts already covers the useful case.

No red flags. The k9s-derived gaps are captured in the new G-theme
candidates below.

## Candidate features for a10r

Each candidate names: source tool(s), current a10r status, and the
proposed mapping. Items are grouped by theme; within a theme they are
ordered roughly by impact / effort ratio. Themes themselves are not
ranked against each other.

### A. CLI surface (non-TUI mode)

#### A1. `a10r doctor` — preflight health checks
- **Source:** chezmoi `internal/cmd/doctorcmd.go:33-70` (pluggable
  `check` interface, severity ladder, tabwriter output); trufflehog
  `pkg/verificationcache` (per-target cached probe pattern).
- **a10r today:** `cmd/validate.go` parses + validates the YAML schema;
  `cmd/info.go` prints config paths and backends. No runtime probe.
- **Proposal:** ship `a10r doctor` with a `Checker` interface
  (`Name() string; Run(ctx) (severity, message)`). Bundled checks:
  per-backend reachability, auth probe (`/-/healthy` + `/-/ready`),
  advertised capabilities vs config (e.g. tenant header on a vanilla
  alertmanager), clock skew vs server (>30s warn), TLS expiry window,
  alertmanager version floor (a10r currently floors at 0.28.1 per
  `docs/design/backend-api-audit.md`). Output as tabwriter rows with
  `ok/warning/error` severity, optional `--json`. Exit non-zero on any
  `error` (see A4).

#### A2. Output formatters as a `Printer` strategy: `--output json|yaml|table`
- **Source:** trufflehog `pkg/output/{json,plain,github_actions}.go`
  with a `Printer` interface and a `PrinterDispatcher` adapter; duf
  `main.go:212-218` early-branches `--json` *before* theme/style setup.
- **a10r today:** TUI-only output. No headless `alerts list` / `silences
  list`.
- **Proposal:** introduce a small `output.Printer` interface
  (`Header()/Row()/Footer()`) and ship three implementations: `table`
  (lipgloss for TTY, plain for pipes), `json` (struct tags on the
  existing `internal/backend` types), `yaml`. Every read-only command
  (`alerts list`, `silences list`, `groups list`, `receivers list`,
  `status`) takes `--output` with `table` as TTY default, `json` as
  pipe default. This unlocks scripting around alerts without parsing
  TUI output.

#### A3. Init wizard `a10r init`
- **Source:** chezmoi `internal/cmd/initcmd.go:86-123`, with template
  funcs `promptString`/`promptChoice` registered only during init
  (`config.go:950-964`).
- **a10r today:** users hand-write YAML from `examples/`. No bootstrap.
- **Proposal:** `a10r init` interactively prompts for backend kind
  (alertmanager / mimir), URL, auth mode, optional tenant, default
  poll interval, theme. Writes
  `$XDG_CONFIG_HOME/a10r/config.yaml`. Add a `--one-shot` flag for
  CI/CD provisioning that takes a flat KV input.
  *(Superseded — the backend-kind question and its tenant/prefix
  sub-prompts were dropped by ADR 0039; the shipped wizard asks
  name, URL, auth, poll, and theme, with a post-write footer for
  Mimir discoverability.)*

#### A4. Distinct exit codes for CI integration
- **Source:** trufflehog `main.go` (`os.Exit(183)` when results found
  + `--fail`, `0` clean, `1` error).
- **a10r today:** `cmd/validate.go` returns 0/1 only; no semantic
  exits elsewhere.
- **Proposal:** define a small exit-code table in `cmd/exit.go` —
  e.g. `0` ok, `1` runtime error, `2` config invalid, `3` unreachable
  backend, `10` `--fail`-style "alerts firing matched filter".
  Document in `docs/end-users/exit-codes.md`. Lets on-call wrappers
  do `a10r alerts list --severity=critical --fail || page-oncall`.

#### A5. Verbose / debug HTTP request dumps with redaction
- **Source:** chezmoi structured `slog` setup
  (`internal/cmd/config.go:265,378,2293-2301`); trufflehog
  `pkg/log/redaction_core.go` and `dynamic_redactor.go` wrapping zap
  with global redactors registered at startup.
- **a10r today:** `internal/log` already uses slog with logfmt/json,
  level control via `--debug/--quiet`. **No request/response dump
  mode and no redaction layer.**
- **Proposal:** add `--debug-http` that logs request line, headers,
  bodies (capped). Redact `Authorization`, `Cookie`, `X-Scope-OrgID`,
  any header marked secret in the auth config, and the bearer token
  itself, via a redactor list registered at logger init. Pattern: a
  `redaction` package that all sinks pass through, never write raw
  secrets to the log file (`~/.local/share/a10r/a10r.log`).

#### A6. Cobra command grouping with `GroupID` and aliases
- **Source:** chezmoi `internal/cmd/config.go:1878-2018` groups 40+
  subcommands by `daily / advanced / encryption / migration`; aliases
  on individual commands.
- **a10r today:** four flat subcommands (`validate`, `version`, `info`,
  `completion`).
- **Proposal:** as new commands land (`doctor`, `init`, `alerts`,
  `silences`, `receivers`, `status`), assign `GroupID` so `a10r --help`
  reads as: *Read* (`alerts`, `silences`, `groups`, `receivers`,
  `status`), *Write* (`silence create`, `silence expire`),
  *Diagnostics* (`doctor`, `validate`, `info`, `version`),
  *Setup* (`init`, `completion`). Aliases like `sil` → `silences` keep
  parity with the in-TUI `:` command bar (which already aliases
  `sil/silences`).

#### A7. Pager integration with `--no-pager`
- **Source:** chezmoi persistent flags in `config.go:1920-1921` plus
  `getDiffPagerCmd()` pattern (respects `$PAGER`, falls back to less).
- **a10r today:** no pager handling; long table output overflows.
- **Proposal:** for `alerts list` / `silences list` in `--output=table`
  on a TTY, pipe through `$PAGER` (default `less -FRX`). Add
  `--no-pager` persistent flag. Skip when `--output != table` or stdout
  is not a TTY.

#### A8. End-of-run metrics summary
- **Source:** trufflehog final
  `logger.Info("finished scanning", chunks=…, bytes=…, secrets=…,
  duration=…)`.
- **a10r today:** none (TUI runs forever; CLI commands are
  short-lived). Not yet useful.
- **Proposal:** for any future long-running CLI (e.g. `a10r silences
  expire --filter=…` against many tenants), emit a final summary log
  line with `tenants_queried`, `silences_matched`, `silences_expired`,
  `errors`, `duration`. Useful for cron and CI logs.

#### A9. Recursive config-dir discovery with symlink follow
- **Source:** lfk merges `~/.kube/config` + `~/.kube/config.d/*`
  (recursive, symlinks followed) + `KUBECONFIG` env
  (`README.md:75-76`).
- **a10r today:** single-file config at
  `$XDG_CONFIG_HOME/a10r/config.yaml`; no `.d/` merging. Adding a
  tenant means hand-editing one file.
- **Proposal:** load `$XDG_CONFIG_HOME/a10r/config.d/*.yaml`
  (recursive, symlinks followed, lexical order) and merge onto the
  base file. Last-key-wins for scalar overrides; backends
  de-duplicated by name with a precise "duplicate name" error on
  conflict. Lets ops teams ship per-environment tenant snippets via
  configuration management without touching the user's hand-edited
  base. Honour `A10R_CONFIG_DIR` for the .d location.

### B. TUI layout & rendering

#### B1. Min-width fallback / responsive degradation
- **Source:** superfile `src/internal/model_render.go:23-44`
  (`terminalSizeWarnRender`); validated `sidebar_width` 5-20 or 0
  (`config_type.go:62`).
- **a10r today:** `internal/tui/app/app_test.go` shows
  `WindowSizeMsg` is handled, but no minimum-width fallback; very
  narrow terminals corrupt the k9s 3-column shortcut grid landed in
  commit `404f9b5`.
- **Proposal:** define `panel.MinWidth` / `panel.MinHeight`
  thresholds. Below threshold, render a centered "terminal too small —
  resize to ≥ X cols" placeholder instead of the normal panel. Same
  pattern at the page level: hide right-hand columns first, then
  collapse to single-column list before showing the placeholder.

#### B2. ASCII / Unicode capability detection with auto-fallback
- **Source:** duf `style.go:5-14` probes `runewidth.RuneWidth('╭')`
  at startup and degrades to ASCII if the terminal mis-renders
  box-drawing; user override via `--style ascii|unicode`.
- **a10r today:** lipgloss is used unconditionally; on stripped fonts
  or restrictive SSH/jumpbox setups, borders and severity glyphs may
  break.
- **Proposal:** at TUI bootstrap, run the same probe; expose
  `tui.glyph_set: auto|unicode|ascii` in the YAML config and a
  `--ascii` CLI flag. Theme file picks the rune set from a small
  table (e.g. `severityIcon.crit = "▲"` vs `"!"`). Keep the borders
  thin or disabled in ASCII mode.

#### B3. Severity gradient: 3-tier threshold colour mapping
- **Source:** duf `table.go:388-400` switches FG colour based on usage
  thresholds (yellow at 0.5, red at 0.9); thresholds are flag-driven.
- **a10r today:** alert SEVERITY cell is themed (`411acac`) but as a
  per-state lookup, not a continuous gradient; group sizes / silence
  countdowns have no visual emphasis.
- **Proposal:** apply the same 3-tier mapping to numeric columns:
  alert age (info → warn → red as it ages), silence remaining time
  (green → yellow → red as it elapses), group cardinality (green →
  yellow → red over thresholds). Thresholds live in theme YAML so
  users can tune them; defaults align with on-call gut feel.

#### B4. Hidden numeric sort columns
- **Source:** duf `table.go:272-294` defines 19 columns where 12-19
  are hidden numeric helpers used purely for sort ordering.
- **a10r today:** **already shipped** — `tablesort.Column[T].Less`
  takes a typed comparator on the entry pointer (not on the rendered
  cell), so alerts sort on `StartsAt.Before` (alert.go:87), silences
  on `StartsAt`/`EndsAt` (silences.go:66,71), groups on `len(Alerts)`
  / `severityRank` (groups.go:56,69). The duf "extend cell to
  `{Display, SortKey}`" pattern doesn't apply — a10r is past it.
- **Status:** DONE (2026-05-08 triage). No work remaining.

#### B5. Smart column-width allocation
- **Source:** duf `table.go:191-269` — measures content per column,
  reserves fixed widths for narrow columns, distributes the
  remainder across dynamic columns by per-column weights (mountpoint
  40 %, fs 40 %, type 20 %).
- **a10r today:** k9s-style table from
  `docs/design/k9s-look-and-feel.md` uses fixed/percentage widths;
  unbounded label values can blow out the layout.
- **Proposal:** lift the weight-driven distributor for the alerts
  page (labels are the unbounded column). Rest of the table shrinks
  proportionally; labels truncate with ellipsis and full content is
  still available in the alert detail page.

#### B6. Dual-layer bar rendering (visual + numeric fallback)
- **Source:** duf `table.go:343-423` renders Unicode block bars but
  always prints `%.1f%%` next to them; ASCII mode degrades to
  `[####...] 12.3%`.
- **a10r today:** no progress/proportion visualisation.
- **Proposal:** for the status page (per-tenant: alert count vs
  threshold, ring health, notification queue depth) render a small
  inline bar plus numeric so the same widget works in both glyph
  modes.

#### B7. Sidebar with sections (tenants / saved filters / pinned alerts)
- **Source:** superfile `src/internal/ui/sidebar/sidebar.go:14-57`
  with sections (home / pinned / disks) and a search box.
- **a10r today:** tenant scope is held in the header (numeric
  switcher + Ctrl+T picker). Works, but doesn't scale to 20+
  tenants and doesn't surface saved filters or pinned alert
  groups.
- **Proposal:** a togglable left sidebar (`Ctrl+B`) with sections
  *Tenants*, *Saved filters*, *Pinned*. Off by default to preserve
  the current k9s look; on, it's a navigation aid for users with
  large tenant fleets. Reuse existing `internal/tui/keys/dispatch.go`
  precedence layers for sidebar focus.

#### B8. Explicit sort UX: cycle, flip, reset
- **Source:** lfk
  `internal/ui/config_keybindings.go:44-47,122` binds SortNext (`>`),
  SortPrev (`<`), SortFlip (`=`), SortReset (`-`); README documents
  the contract verbatim. Pairs with the column-toggle overlay (`,`,
  see G2 amendment) so the user can see the active sort key while
  cycling.
- **a10r today:** **mostly shipped** — `tablesort.Sorter[T]` already
  exposes Shift+letter to select a column, repeating the active
  column flips ASC↔DESC, `h`/`l` walks columns left/right with
  wrap-around, and the active column renders an arrow glyph (sorter.go
  comment block 1-17). The four-key lfk contract maps onto a10r as:
  `<` ≈ `h`, `>` ≈ `l`, `=` ≈ "press the active column's hotkey
  again". Only **reset to page default** (lfk's `-`) is missing.
- **Status:** microfix — add a `Reset()` to `tablesort.Sorter[T]`
  bound to `-` (or similar). Not a feature, no ADR. Wave 2 alongside
  D4 / A6.

### C. Theme & config

#### C1. Light/dark autodetection via termenv
- **Source:** duf `themes.go:31-78` uses
  `termenv.EnvColorProfile()` and inspects `COLORFGBG` /
  `NO_COLOR` to pick `dark`, `light`, or `ansi` themes
  automatically.
- **a10r today:** users explicitly pick a theme in YAML or via
  `--theme`; no autodetection, three themes shipped
  (catppuccin-mocha, catppuccin-latte, gruvbox-dark).
- **Proposal:** `theme: auto` in YAML maps to dark when the terminal
  background is dark, light otherwise. Honour `NO_COLOR` (drop to
  the existing `gruvbox-dark` palette stripped of colour, or a
  bundled `nocolor` skin). Keep explicit theme as the override.

#### C2. Hot-reload config on file change
- **Source:** wtf `app/wtf_app.go:209-250` watches the config path
  with `radovskyb/watcher`; on event, rebuild a fresh `WtfApp`
  preserving the underlying terminal app handle.
- **a10r today:** SIGHUP isn't handled; config is read once at
  startup.
- **Proposal:** watch the resolved config file and the `skins/` dir.
  On change, re-run validation; if valid, swap pollers and theme
  atomically, flash an info toast (`config reloaded`); if invalid,
  flash an error with the validation message and keep the old
  config. Gate behind `config.hot_reload: true` (default off so
  we don't introduce a watcher in CI / one-shot runs).

#### C3. User-overridable theme & keybinding files
- **Source:** superfile ships defaults under
  `src/superfile_config/theme/*.toml` and
  `src/superfile_config/hotkeys.toml`; users drop overrides under
  their config dir.
- **a10r today:** themes already follow this pattern
  (`<config-dir>/skins/<name>.yaml`, `docs/design/theming.md`).
  **Keybindings are not user-overridable** — `docs/design/
  open-questions.md` explicitly defers this.
- **Proposal:** unblock the deferred item. Schema:
  `<config-dir>/keys/<profile>.yaml` listing
  `<action>: [keys...]`. Defaults from `internal/tui/action`
  registry; user file shadows. Detect conflicts at load (same key
  bound to two enabled actions) and refuse to start with a precise
  error. Reuse trufflehog's "fail closed with location" approach
  for messages.

#### C4. Per-page colour overrides
- **Source:** wtf `cfg/common_settings.go:55-119` lets each module
  override the global `ColorTheme` (border / row / text / widget).
- **a10r today:** theme schema in `internal/tui/theme/schema.go:22-51`
  has fixed roles; no per-page override.
- **Proposal:** allow `theme.pages.<name>.<role>` overrides in the
  user skin. Useful for users who want, e.g., the silences page in
  a different accent so it's visually distinct from alerts at a
  glance. Low priority — ship only if requested.

#### C5. Runtime dark/light response via CSI 996/2031
- **Source:** lfk `internal/ui/colormode.go:13-18,32-49,55-60` —
  `EnableColorModeCmd` writes `CSI ?2031h` (subscribe to Color
  Palette Update Notifications) plus `CSI ?996n` (query current
  preference); `ParseColorModeMsg` matches `CSI ?997;1n` (dark) /
  `CSI ?997;2n` (light) reports and `SetColorMode`
  (`colormode.go:72-86`) swaps the active scheme atomically. README
  flags this as Ghostty / kitty ≥ 0.27 / Contour territory.
- **a10r today:** C1 proposes startup-only autodetection via
  `termenv`; nothing reacts to OS appearance changes after launch.
- **Proposal:** layer the runtime listener on top of C1. When the
  user picks `theme: auto` *and* sets `theme.dark` / `theme.light`,
  emit the subscribe sequence at TUI start and route incoming
  unknown-CSI bytes through a parser equivalent to lfk's; on report,
  swap themes without restart. Disable on exit (`CSI ?2031l`). No
  new YAML field beyond `theme.dark` / `theme.light` — `theme: auto`
  drives both startup detect and live response. Falls back silently
  when the terminal doesn't support the protocol.

#### C6. Theme codegen from external palette library
- **Source:** lfk `cmd/themegen/main.go` ingests ghostty themes;
  generated output `internal/ui/colorschemes_gen.go` (~167 KB)
  embeds 460+ schemes consumed by `internal/ui/colorschemes.go`
  via `BuiltinSchemes()`.
- **a10r today:** five hand-curated skins under
  `internal/tui/theme/skins/`; adding a theme requires hand-mapping
  every role.
- **Proposal:** add `tools/themegen/` (Go program, not a runtime
  dep) that walks a vendored ghostty-theme snapshot and emits
  a10r-shaped skin YAMLs into `internal/tui/theme/skins/generated/`.
  Mapping from terminal palette to a10r's role schema
  (`header.bg`, `severity.crit`, etc.) lives in the generator and
  is reviewed by hand. `<config-dir>/skins/` user override path
  unchanged. Refresh on demand, not on every build (no network at
  build time — input is committed). Composes with C1/C5 — once the
  library exists, `theme: auto` has hundreds of dark/light pairs to
  pick from.

#### C7. Per-tenant accent colour with picker
- **Source:** lfk `internal/ui/cluster_color.go:18-27`
  (`ClusterColorNames` — eight stable named colours) and
  `internal/ui/cluster_color_overlay.go:38-78` (swatch picker
  overlay). Persisted across restarts (per
  `config_keybindings.go:99-101`).
- **a10r today:** tenant scope is a string in the header; nothing
  visually differentiates tenants beyond the label. Users running
  prod / pre-prod / dev side by side rely on reading the name.
- **Proposal:** per-tenant `accent: <named-colour>` field; when set,
  the header strip and the cursor row tint to the accent on pages
  scoped to that single tenant (in mixed views, the TENANT column
  takes the colour). A picker overlay (suggested binding `Ctrl+K`)
  toggles the colour without YAML edits and persists to
  `<config-dir>/state.yaml`. Useful for dual-tenant ops where
  mis-targeting a destructive action is the failure mode.

### D. Refresh & resilience

#### D1. Per-page refresh-interval override
- **Source:** wtf `cfg/common_settings.go:48` (`refreshInterval` per
  widget) plus the scheduler in `app/scheduler.go:11-38`.
- **a10r today:** poll interval is per-backend
  (`internal/config/types.go:58-71`), not per-page. Status / receivers
  data changes much less often than alerts.
- **Proposal:** allow `pages.<name>.poll_interval` to override the
  backend default. Sensible defaults: alerts 30s, groups 30s,
  silences 60s, receivers 5min, status 60s. Saves API load on the
  big-tenant deployments without extra config.

#### D2. Per-page error rendering (don't propagate to the whole app)
- **Source:** wtf widgets store `widget.err` and return it as the
  rendered content; the dashboard stays usable.
- **a10r today:** poller emits `BackendStatusMsg` on state transition
  which feeds the header connection indicator; per-page errors (e.g.
  silence write rejected) flash but the page itself doesn't carry
  an error banner.
- **Proposal:** each page model gains an optional `error` field; on
  `RefreshError` the page renders an error band above the table
  ("backend X: 401 unauthorised"), keeping last-known data visible.
  Combine with the existing toast for the human-noticeable signal.

#### D3. Verification cache for tenant probes
- **Source:** trufflehog `pkg/verificationcache` dedupes repeat
  verification work with TTL; per-target timeouts.
- **a10r today:** `a10r doctor` doesn't exist yet (A1); when it
  does, naïvely probing N tenants on every run could be slow.
- **Proposal:** when implementing A1, wrap each check in a small
  in-memory cache keyed by `(tenant, check_name)` with a short TTL
  (30 s). Useful when `doctor` is invoked from the TUI's status
  page or repeatedly from a wrapper script.

#### D4. Watch-mode runtime toggle
- **Source:** lfk `internal/ui/config_keybindings.go:43,121` binds
  WatchMode (`w`) to a runtime flag flipping auto-refresh on / off.
- **a10r today:** D1 proposes per-page intervals; nothing pauses or
  resumes refresh interactively. While reading a long alert detail
  the row can shift under the cursor on the next poll.
- **Proposal:** `w` toggles the active page's poller between its
  configured interval and "manual only" (re-poll on `Ctrl+R`).
  Footer shows `WATCH OFF` while paused, `WATCH 30s` when on. State
  is per-page and per-tab, not global, so pausing the alerts page
  doesn't freeze the silences page in another tab. Composes with
  D1 — toggle just sets the effective interval to ∞.

#### D5. Background-tasks overlay
- **Source:** lfk `internal/ui/overlay_background_tasks.go:11-22`
  defines `BackgroundTaskOverlayMode` with `ModeRunning` /
  `ModeCompleted` states; render path renders live-elapsed and
  completed durations.
- **a10r today:** silence creation, bulk-silence fan-out and config
  reload are async but only flash a toast on settle; no list of
  in-flight or recently-completed work. When a bulk fan-out half
  succeeds it's hard to see *which* tenants are still pending.
- **Proposal:** togglable overlay (suggested binding ``` ` ``` —
  matches lfk) listing in-flight ops (silence create, bulk
  fan-out, doctor probes) with `running / done / failed` status,
  elapsed time, and last error message. Source is a small
  `internal/tui/bgtasks` package fed by every async command;
  retention bounded to the last N completed entries. Pairs with
  A8 (CLI metrics summary — same data shape, different sink).

### E. Interaction patterns

#### E1. Mouse wheel scroll (keyboard-first, mouse-secondary)
- **Source:** superfile `src/internal/model.go:79-84,111` —
  `tea.MouseModeCellMotion`, wheel routed to `wheelMainAction()`,
  no click-to-focus.
- **a10r today:** no mouse handling.
- **Proposal:** enable wheel scroll on table pages and in the help
  modal. Explicitly do **not** add click-to-focus; keep the SRE
  keyboard-first ergonomic. Scope the change to ~30 lines in
  `internal/tui/app`.

#### E2. Multi-panel focus tracker with numeric hotkeys
- **Source:** wtf `app/focus_tracker.go:33-163` auto-assigns 1-9 to
  focusable widgets, draws focused border in a different colour, Tab
  / Shift+Tab cycle.
- **a10r today:** numeric `0-9` are already taken for tenant quick
  switching (`internal/tui/app/app.go:160-181`). No multi-panel pages
  yet, but the alert detail / silences write pages have multiple
  focusable regions handled ad-hoc.
- **Proposal:** introduce a `panel.FocusManager` only for pages with
  >1 focusable region (alert detail with metadata + labels + actions,
  future split views). Use `Tab` / `Shift+Tab` to cycle and a focused
  border colour (already in the theme as
  `header.focused`). Skip numeric hotkeys here — they conflict with
  tenant switching.

#### E3. Modal priority chain
- **Source:** superfile `src/internal/model.go:137-147`
  (`updateRenderForOverlay`) renders the topmost modal: error >
  confirm > main.
- **a10r today:** **bug class is already prevented** — single-modal
  slot (`app/modal.go:41-58`) plus a documented input precedence
  (`app/input.go:265-274`: open modal → prompt → top-page
  input-capture → dispatcher → top-page fallthrough) means at most
  one overlay is ever open, and every key routes to it. The
  "clicked behind a modal" bug class superfile's chain prevents
  doesn't surface in a10r because there's nothing behind a modal
  to click. Help-dismiss-on-any-key is only a problem in a stack;
  with the single slot it can't conflict with another modal.
- **Status:** DONE-by-design (2026-05-08 triage). Outcome is
  already achieved by the single-slot pattern; no implementation
  work needed. Reopen only if a concrete stacking scenario
  materialises (e.g. blocking error popup that must overlay an
  in-flight form). No ADR.

#### E4. Saved queries / filter presets
- **Source:** superfile sidebar pinned items pattern (B7); chezmoi
  template-funcs registered at command-init time (A3).
- **a10r today:** `/` filter is live but ephemeral; the audit notes
  *"No saved queries (not implemented)"*.
- **Proposal:** `:save <name>` from the prompt persists the current
  scope+filter to `<config-dir>/queries.yaml`; `:open <name>` (or
  picking from the sidebar's *Saved filters* section, B7) restores
  it. Tab-complete `:open` from the saved list. Export/import as
  YAML so teams can share filter packs.

#### E5. Command bar suggestions / completion
- **Source:** superfile prompt `src/internal/ui/prompt/type.go:17-38`
  with suggestions list and dual-mode (`>` SPF, `:` shell).
- **a10r today:** **already shipped** — `cmdbar.Resolver.Suggest`
  (cmdbar.go:137) returns the alphabetically-first registered alias
  matching a prefix; the `:` prompt renders this as ghost-text and
  Tab accepts. Mirrors k9s's "tab accepts first suggestion"
  affordance.
- **Status:** DONE. The action-registry hint surfacing
  (description text below the prompt) remains a future affordance,
  but the core suggestion behaviour is in place.

#### E6. In-session tabs (multiple page stacks)
- **Source:** lfk `internal/ui/config_keybindings.go:84-87,140-141`
  defines `NewTab` (`t`), `NextTab` (`]`), `PrevTab` (`[`); tab state
  lives on the app model alongside the resource explorer.
- **a10r today:** single page stack. To compare alerts across two
  tenants, or alerts vs silences side-by-side, the user hops the
  tenant scope or page and loses cursor + filter state.
- **Proposal:** N tabs, each with its own scope + page-stack +
  filter state. `t` opens new (defaulting to the current scope),
  `]`/`[` cycle, the existing `0-9` numeric switcher stays scoped
  to *tenant* (not tab — never overload). A thin tab strip lives
  above the header; hidden when only one tab is open to preserve
  the current k9s look. Tabs are session-only by default; saved
  tabs reuse E4 (saved queries) so they survive restart.

#### E7. Bookmarks with slot keys
- **Source:** lfk `internal/app/bookmarks.go:13-25` persists to
  `$XDG_STATE_HOME/lfk/bookmarks.yaml`; README documents `m<slot>`
  set, `'<slot>` jump with lowercase=context-aware vs
  uppercase=context-free.
- **a10r today:** none. Returning to "the silences page in tenant X
  with filter Y" requires retyping each step.
- **Proposal:** `m<slot>` saves the current
  `(tenant, page, filter, cursor)` to slot `a-z` / `A-Z`;
  `'<slot>` jumps back. Lowercase slots are tenant-scoped (jump
  also restores the tenant); uppercase are tenant-free (only
  page+filter+cursor). Persist to
  `$XDG_STATE_HOME/a10r/bookmarks.yaml`. Composes with E4 — saved
  queries are *named* bookmarks; bookmarks are unnamed quick slots
  for "I'll be back in 2 minutes."

#### E8. Hint bar with rotating startup tips
- **Source:** lfk `internal/ui/hintbar.go:74-77` (`RenderHintBar`)
  plus `internal/ui/config.go:322-323` (`tips: false` opt-out).
- **a10r today:** footer shows status + key hints derived from the
  action registry; nothing surfaces less-used keys to first-time
  users.
- **Proposal:** an additional one-line band below the footer (or
  replacing the footer's hint segment for a few seconds on first
  launch of each session) shows a random tip from a curated list
  ("Press `?` for help", "`/` filters live; `Esc` clears", "Numbers
  `0-9` jump to tenants", "`m<key>` bookmarks the current view").
  Curated list lives in `internal/tui/help/tips.go` next to the
  action registry; new tips land alongside the actions they
  advertise. `tips: false` in config disables. Off entirely once
  the user has run the binary N times (`<config-dir>/state.yaml`
  counter).

### F. Templating, safety, and filtering

#### F1. Template-driven silence comments and notification previews
- **Source:** chezmoi `internal/cmd/config.go:385-386,493-1000`
  registers `sprig.TxtFuncMap()` plus custom funcs at command init.
- **a10r today:** silences accept a free-text comment; no templating.
- **Proposal:** `silences.comment_template` in YAML, evaluated against
  a small data context (`alert`, `user`, `now`, `cluster`). Lets
  teams enforce "INC-####, owner @x, expires Yh" comments via
  template + sprig (e.g. `{{ env "USER" }} via a10r at
  {{ now | date "2006-01-02 15:04Z" }}`). Same engine reused later
  for receiver test payloads and notification dry-run previews.

#### F2. Path / label filter composition: regex + globs
- **Source:** trufflehog `main.go` flags
  `--include-paths` (regex), `--exclude-paths` (regex),
  `--exclude-globs` (glob, applied early as a fast filter).
- **a10r today:** alert filter is substring across labels and
  annotations (`docs/end-users/keybindings.md`); silence list takes
  Prometheus-style matchers (`internal/backend/filter.go`). No
  layered filters.
- **Proposal:** `:filter` accepts both glob (cheap, applied first
  client-side) and regex (precise, second pass). Syntax:
  `:filter team=plat-* severity=~"crit|warn"`. Use the same parser
  for `silences list --filter` headless.

#### F3. Custom verifier endpoints / per-tenant overrides
- **Source:** trufflehog `--verifier` map and optional
  `EndpointCustomizer` interface (`pkg/detectors/detectors.go`).
- **a10r today:** per-backend transport config exists; no per-check
  endpoint override (relevant once `doctor` lands).
- **Proposal:** `doctor.checks.<name>.endpoint` per backend so a
  Mimir tenant on a separate health URL than its alert API can be
  probed correctly. Composes cleanly with A1 + D3.

#### F4. Search-mode auto-detection (substring / regex / fuzzy / literal)
- **Source:** lfk `internal/ui/search.go:24-49` (`DetectSearchMode`):
  `~` prefix → fuzzy, `\` prefix → literal substring (escapes regex
  meta), regex metacharacters present → regex, otherwise substring.
  Regex metacharacters checked at lines 51-60.
- **a10r today:** F2 proposes glob + regex as a layered filter; the
  current `/` filter is substring only. Glob is implicit when no
  metachars are present, but there's no explicit "literal" escape
  for label values that legitimately contain regex chars (e.g.
  `networking.istio.io`).
- **Proposal:** adopt the four-mode auto-detect verbatim for the
  `/` filter and the headless `:filter` flag. F4 supersedes F2 on
  the user-input side; F2's label-selector parser
  (k9s-style `team=plat,severity!=info`) stays for the structured
  path. Note the regex auto-detect can fire on legitimate label
  values containing `.` — mitigate by treating `.` alone (no other
  regex meta) as substring; require a second regex char to flip
  the mode. Document the rule in `docs/end-users/keybindings.md`
  next to the existing filter docs.

### G. k9s-derived (extension surface)

These come specifically from the k9s scout. They extend a10r along
the same axes k9s exposes for its power users — plugin commands,
column views, user aliases, fish-style suggestions on filters — none
of which are presently in a10r.

#### G1. Plugin system: YAML-defined external commands per page
- **Source:** k9s `internal/config/plugin.go` defines `Plugin`
  with `Shortcut` (key), `Description`, `Scopes` (which views the
  plugin applies to), `Command` + `Args` (interpolated with row
  context), `Background`, `Confirm`, `Dangerous`, `Inputs`
  (interactive prompts before run). Loaded from
  `<config-dir>/plugins.yaml` or `plugins/*.yaml`.
- **a10r today:** the action registry is in-binary only; no surface
  for users to bind a key to an external command.
- **Proposal:** ship `<config-dir>/plugins.yaml` with the same
  schema. Concrete a10r-fitting plugins users would write:
  *open alert in Grafana* (key `g`, scope `alerts`,
  `xdg-open https://grafana/d/.../{{ .labels.alertname }}`), *copy
  alert as PromQL* (`y`, scope `alerts`, pipe to `wl-copy/pbcopy`),
  *page on-call via webhook* (`p`, scope `alerts`, `Dangerous: true`,
  `Confirm: true`, `Args: [-d, '{{ . | toJson }}']`). Reuse the
  templating engine from F1 for arg interpolation. Honour read-only
  mode by skipping `Dangerous: true` plugins, same way the action
  registry already does. This is the single biggest k9s-shaped gap
  in a10r and unblocks team-specific workflows without code changes.

#### G2. User-defined views: per-page column subsets
- **Source:** k9s `views.yaml` lets users override which columns
  appear on each resource view; column order, hidden flag, sort key
  per column. Implementation in `internal/config/views/`.
- **a10r today:** alert / silence / group / receiver tables have
  fixed columns chosen by `docs/design/k9s-look-and-feel.md`. No
  user-side override.
- **Proposal:** `<config-dir>/views.yaml` keyed by page name, value
  is `columns: [<name>...]` with optional `hidden: [...]` and
  `default_sort: <name>`. Useful when a tenant carries domain
  labels (`team`, `service`, `pillar`) that someone wants hoisted
  into the table without touching code. Composes with B4 (hidden
  numeric sort columns) — `views.yaml` decides *which* columns,
  B4 decides *how they sort*.

#### G3. User-defined command aliases
- **Source:** k9s `internal/config/alias.go` + `internal/dao/alias.go`
  load `<config-dir>/aliases.yaml` and merge into the resolver.
- **a10r today:** built-in aliases (`alerts`, `sil`, `gr`, `rec`,
  `q`) are hard-coded in `internal/tui/cmdbar/cmdbar.go`; no user
  surface to add `:gh` → `:groups firing` or shorten frequent flows.
- **Proposal:** `<config-dir>/aliases.yaml` of the shape
  `{<short>: <expanded command>}`. Resolver loads at startup;
  conflicts with built-ins fail closed with a precise message
  (same approach as C3 keybinding conflicts). Pairs naturally with
  E4 (saved queries) — saved queries can be re-exposed as aliases.

#### G4. Fish-style suggestions on the filter prompt
- **Source:** k9s `internal/model/fish_buff.go:19-120` (`FishBuff`
  with `SetSuggestionFn`, `CurrentSuggestion`, `Next/PrevSuggestion`)
  wired into the filter prompt by `internal/view/browser.go:118-148`
  (`suggestFilter` closure replaying recent filters as suggestions).
- **a10r today:** the `/` filter prompt is ephemeral; filters typed
  in the previous session — even ones from 30 seconds ago — are gone.
  E5 already covers `:` command suggestions; this is the **filter**
  side.
- **Proposal:** keep an in-memory ring of the N most recent filter
  expressions, persisted across restarts. On `/`, Tab and arrow
  keys cycle (matches k9s exactly). Take lfk's
  one-ring-per-matcher-class refinement
  (`internal/app/history.go:23-26,39-57`): separate plain-text
  files under `$XDG_STATE_HOME/a10r/` —
  `cmd-history` (`:` command bar; G3 aliases share this),
  `filter-history` (`/` substring/regex/fuzzy filter, shared across
  pages because the matcher is identical),
  `silence-matcher-history` (silences page Prom-matcher input —
  separate parser, separate ring). Plain text, 0o600, last-N
  capped, last-write-wins. Sharing a ring only when the matcher is
  identical avoids polluting one prompt with irrelevant history
  from another. ~120 lines on top of the existing
  `internal/tui/footer/prompt.go`.

#### G5. Generic raw-YAML / JSON detail toggle
- **Source:** k9s `internal/view/details.go:29-61` is a generic
  text/YAML viewer attached to any resource via `y` ("yaml"); a
  read-only viewport with copy-friendly output.
- **a10r today:** alert detail page renders structured fields
  (labels, annotations, generator URL); no escape hatch to the raw
  alertmanager payload for debugging "wait, what does the API
  actually return?" moments.
- **Proposal:** add a `y` binding on the alert / silence / group
  detail pages that toggles between the structured rendering and
  the raw YAML payload. Reuse the existing viewport component;
  the only new code is the marshal step. Power-user feature, low
  cost.

### Cross-references

| Candidate | Theme | Source tools | Effort* | Depends on |
|-----------|-------|--------------|---------|------------|
| A1 `doctor` | CLI | chezmoi, trufflehog | M | — |
| A2 output formatters | CLI | trufflehog, duf | M | — |
| A3 `init` wizard | CLI | chezmoi | M | — |
| A4 exit codes | CLI | trufflehog | S | — |
| A5 redacted HTTP debug | CLI | chezmoi, trufflehog | S | — |
| A6 cobra grouping | CLI | chezmoi | S | A1, A2, A3 (more cmds) |
| A7 pager | CLI | chezmoi | S | A2 |
| A8 metrics summary | CLI | trufflehog | S | future bulk cmds |
| A9 config-dir merge | CLI | lfk | S | — |
| B1 min-width fallback | TUI | superfile | S | — |
| B2 ASCII fallback | TUI | duf | M | C1 (theme aware) |
| B3 severity gradient | TUI | duf | S | — |
| B4 hidden sort columns | TUI | duf | M | — |
| B5 smart column widths | TUI | duf | M | — |
| B6 inline bars | TUI | duf | S | B2 |
| B7 sidebar | TUI | superfile | L | E4 |
| B8 sort UX | TUI | lfk | S | B4, G2 |
| C1 theme autodetect | Theme | duf | S | — |
| C2 hot-reload config | Theme | wtf | M | — |
| C3 user keybindings | Theme | superfile | M | unblocks open-q |
| C4 per-page colour | Theme | wtf | S | — |
| C5 runtime dark/light CSI | Theme | lfk | S | C1 |
| C6 theme codegen | Theme | lfk | M | — |
| C7 per-tenant accent | Theme | lfk | S | — |
| D1 per-page refresh | Refresh | wtf | S | — |
| D2 per-page errors | Refresh | wtf | S | — |
| D3 probe cache | Refresh | trufflehog | S | A1 |
| D4 watch toggle | Refresh | lfk | S | D1 |
| D5 bg-tasks overlay | Refresh | lfk | M | A8 |
| E1 mouse wheel | UX | superfile | S | — |
| E2 focus tracker | UX | wtf | M | — |
| E3 modal priority | UX | superfile | S | — |
| E4 saved queries | UX | superfile, chezmoi | M | — |
| E5 command suggestions | UX | superfile | S | — |
| E6 in-session tabs | UX | lfk | L | E4 |
| E7 bookmarks | UX | lfk | S | — |
| E8 hint bar | UX | lfk | S | — |
| F1 template silences | Templ | chezmoi | M | — |
| F2 layered filters | Templ | trufflehog, k9s | M | F4 supersedes user side |
| F3 per-tenant probe URL | Templ | trufflehog | S | A1 |
| F4 search-mode autodetect | Templ | lfk | S | replaces user side of F2 |
| G1 plugin system | k9s | k9s | M | F1 (templating) |
| G2 user views.yaml | k9s | k9s | S | B4 (sort keys) |
| G3 user aliases | k9s | k9s | S | — |
| G4 filter suggestions | k9s | k9s | S | — |
| G5 raw YAML toggle | k9s | k9s | S | — |

*S = under a day, M = 1-3 days, L = >3 days, on top of tests and review.

---

## Triage outcome (2026-05-08)

Walk-through of the 46 candidates against the v0.0.1 launch lens:
**credibility (no embarrassments)** + **launch story / k9s
positioning**. Pre-launch batch capped aggressively. Each candidate
received an explicit call: ASAP / DEFER / DROP / DONE.

### ASAP — pre-launch batch (11)

Lands before the v0.0.1 flip in two execution waves.

**Wave 1 — independent foundations (7):** A1, A2, A3, A5, B1, D1,
D2.

**Wave 2 — composes on Wave 1 (4):** A4 (after A1+A2), A6 (after
A1+A3), A7 (after A2), D4 (after D1). Plus **B8 microfix** (`-`
reset key on the existing `tablesort.Sorter[T]`) — slot it
alongside Wave 2.

ADRs landed from this triage (per the strict bar — hard to
reverse + surprising + real trade-off):
- **A5** → `docs/adr/0008-http-debug-redaction.md`. Redaction lives
  in `slog.HandlerOptions.ReplaceAttr` (stdlib hook); fixed
  lowercase secret-key set; `X-Scope-OrgID` deliberately not
  masked (Mimir routing key, not credential); headers-only, no
  body dumps in v0.0.1.
- **A4** → `docs/adr/0009-exit-code-table.md`. Codes 0/1/2/3/4/10
  with auth-fail (4) distinct from unreachable (3); partial-failure
  across multi-tenant scope is lenient (exit 0 + stderr warnings);
  codes 3/4 fire only when every tenant failed the same way.

**A2 stability promise:** v0.0.1 ships JSON output with an explicit
"format may change pre-v1" note in `docs/end-users/`. No ADR until
we promise stability.

### DONE — already shipped (4)

Codebase verification revealed the scout doc was stale on these:

- **B4** hidden numeric sort columns — `tablesort.Column[T]` already
  uses typed comparators on entry pointers (alert.go:87,
  silences.go:66,71, groups.go:56,69); the duf cell-extension
  pattern doesn't apply.
- **E5** command bar suggestions — `cmdbar.Resolver.Suggest`
  (cmdbar.go:137) ships ghost-text Tab completion for aliases.
- **E3** modal priority chain — single-modal slot
  (`app/modal.go:41-58`) plus documented precedence
  (`app/input.go:265-274`) already prevents the "clicked behind a
  modal" bug class; no stack needed in v0.0.1.
- **B8 (most of it)** sort UX — same-column flip, `h`/`l` walk,
  arrow glyph, Bindings list all in `tablesort.Sorter[T]`. Only
  the `-` reset key is missing, hence B8's microfix slot in Wave 2.

### DEFER — post-launch roadmap (17)

Real value, not pre-launch material. Order roughly by likelihood of
landing in early v0.1.x:

A9 config-dir merge, B5 smart column widths, B6 inline bars,
B7 sidebar (long-defer), C3 user keybindings, D3 probe cache,
E1 mouse wheel, E2 focus tracker (waits on first multi-region
page), E4 saved queries, **E8 hint bar (must be optional via
config)**, F1 template silences, F2 layered filters, F4 search-mode
autodetect, G1 plugin system, G3 user aliases, G4 filter
suggestions, G5 raw YAML toggle.

### DROP — explicitly out of scope (14)

Not on the roadmap. Re-open only on concrete user signal.

A8 metrics summary (no long-running CLI yet), B2 ASCII fallback,
B3 severity gradient (severity cell already themed), C1 theme
autodetect (`--theme` works), C2 hot-reload config, C4 per-page
colour, C5 runtime CSI dark/light, C6 theme codegen, C7 per-tenant
accent, D5 bg-tasks overlay, E6 in-session tabs, E7 bookmarks,
F3 per-tenant probe URL, G2 views.yaml.

---

## Per-tool sections

### `derailed/k9s`

The explicit reference for a10r's look-and-feel
(`docs/design/k9s-look-and-feel.md`). a10r already mirrors the core
patterns (frame, page stack, command bar, action registry, read-only
mode); see "Alignment check" upfront. This section captures only the
files relevant to the k9s-derived candidates and the surfaces a10r
hasn't picked up yet.

**Key files**
- `internal/view/app.go:45-59` — `App` struct: `Content` PageStack,
  command bar wiring, signal handling. The shape a10r mirrors.
- `internal/view/page_stack.go:14-57` — `PageStack` over
  `tview.Pages`, `StackListener` interface for push/pop callbacks.
- `internal/view/command.go:34-68` — `Command` struct backed by
  `dao.Alias` for resource-name resolution.
- `internal/dao/alias.go` + `internal/config/alias.go` — alias
  loading from `<config-dir>/aliases.yaml` (G3).
- `internal/config/plugin.go` — `Plugin` struct with `Shortcut`,
  `Scopes`, `Command`, `Args`, `Background`, `Confirm`,
  `Dangerous`, `Inputs`. Loaded from `plugins.yaml` /
  `plugins/*.yaml` (G1).
- `internal/config/views/` — `views.yaml` schema for per-resource
  column subsets (G2).
- `internal/model/fish_buff.go:19-120` — `FishBuff` with
  `SetSuggestionFn` and prev/next cycling. Used by both command
  bar and filter prompt (G4).
- `internal/view/browser.go:118-148` — `suggestFilter` closure
  replays recent filters as suggestions for the `/` prompt (G4).
- `internal/ui/action.go:32-46,159-169` — `KeyAction` struct
  (`Description`, `Handler`, `Opts`); `ClearDanger` strips
  `Dangerous`-flagged actions in read-only mode. a10r's
  decoupled equivalent is `internal/tui/action/action.go:32-57`.
- `internal/model1/table_data.go:143-219` — `Filter` with fuzzy /
  regex / label-selector branches; `IsLabelSelector` check at
  149-164 routes to `k8s.io/apimachinery/pkg/labels` parsing
  (informs F2).
- `internal/view/cmd/interpreter.go:62-80` — parses
  `<resource> -l <selector> @<namespace>` from a single typed
  string. Pattern for a10r: `<page> <filter> @<tenant>`.
- `internal/view/details.go:29-61` — generic text/YAML viewer,
  attached as a `y` binding on resource views (G5).
- `internal/view/xray.go:39-57` — hierarchical drill-down
  (intentionally *not* adopted; alertmanager has no tree).
- `internal/config/k9s.go:43,410-417` — `IsReadOnly` config check
  shape; informs how a10r should expose plugin / alias
  read-only filtering (already aligned).
- `internal/config/styles.go` + `skins/*.yaml` — themes; pattern
  a10r already mirrors via `internal/tui/theme/loader.go`.
- `internal/slogs/` — slog setup; both projects converged
  independently on the same pattern.

**Patterns we're borrowing** — see G1 (plugins), G2 (views.yaml),
G3 (user aliases), G4 (filter suggestions), G5 (raw YAML toggle).
F2 (label-selector parsing) gets material weight from k9s's
production use of `k8s.io/apimachinery/pkg/labels`.

**Patterns a10r already mirrors** (see "Alignment check" upfront for
file:line refs): three-zone frame, page stack with Esc, command bar
with prefix-resolved aliases, action-registry-driven help overlay,
read-only mode stripping dangerous actions, theme files with user
override, numeric quick-switch + picker, vim motions, structured
slog logging.

**Skip**
- The 30+ resource-type views (pods, deployments, services…) —
  domain-specific to Kubernetes; a10r's focused alert/silence/group
  set is correct.
- Port-forward, exec, scale, drain, edit-yaml-then-apply —
  Kubernetes cluster administration; no alertmanager analogue.
- kubeconfig parsing, in-cluster watch streams — data sources
  irrelevant to a10r's HTTP API client.
- `tview/tcell` internals — bubbletea/lipgloss is the deliberate
  choice for a10r and is more testable.
- Xray hierarchical drill-down — alerts → groups is N-to-M, not
  tree-shaped; the page stack already covers the realistic flow.
- Benchmark and port-forward background panels — no analogue.
- Mouse-driven cluster admin — keep keyboard-first per the SRE
  ergonomic.

### `muesli/duf`

CLI disk-usage tool with a heavily styled table. Useful for column
layout, severity gradient colouring, terminal-capability fallbacks,
and the JSON-output decoupling pattern.

**Key files**
- `main.go` — CLI entry; flag parsing; `--json` early branch before
  theme/style setup; terminal width detection (`term.IsTerminal` +
  `term.GetSize` with safe fallback to 80).
- `table.go` — the meat: column-width computation
  (lines 191-269), severity-gradient FG colour
  (lines 388-400), block-bar transformer with bg-colour fill
  (lines 343-423), hidden numeric sort helpers (lines 272-294).
- `themes.go` — three bundled themes (dark / light / ansi); termenv
  environment-color profile detection.
- `style.go` — the runtime rune-width probe that toggles ASCII /
  Unicode mode (lines 5-14).
- `groups.go` — partitioning rows into multiple sub-tables with
  per-group filter predicates.

**Patterns we're borrowing** — see B3, B4, B5, B6, B2, C1, A2.

**Skip**
- Filesystem-type icons — duf doesn't render them; n/a anyway.
- Platform-specific `mounts_*.go` discovery — a10r gets data over
  HTTP; no analogue.
- `--inodes` flag and inode metric branch — alertmanager has no
  equivalent.
- `IGLOU-EU/go-wildcard` glob lib — Go's `path.Match` is plenty
  for our globs.
- `muesli/mango` + `muesli/roff` man-page generator — a10r's help
  is auto-generated from the action registry already.

### `wtfutil/wtf`

Terminal dashboard with a widget/grid model on top of tview/tcell.
Useful for the "small focused refreshable widget" abstraction, the
focus tracker, config hot-reload, and the per-widget error pattern.

**Key files**
- `wtf/wtfable.go` — the `Wtfable` interface (Enablable + Schedulable
  + Stoppable) every widget implements (lines 9-26).
- `view/base.go` — `Base` struct mixed into widgets: enable/disable
  mutex, refresh interval, help text, border colour per focus state
  (lines 13-181).
- `app/scheduler.go` — `Schedule(widget)`: one timer goroutine per
  widget, ticks `RefreshInterval` until `widget.QuitChan()` closes
  (lines 11-38).
- `app/focus_tracker.go` — auto-assigns 1-9 to focusable widgets in
  visual order, Tab / Shift+Tab cycles, blur/focus mutates border
  colour (lines 33-163).
- `app/display.go` — grid layout via `tview.Grid.AddItem(top, left,
  height, width, …)` driven by per-widget `PositionSettings` (lines
  17-67).
- `app/wtf_app.go` — `watchForConfigChanges` watcher that rebuilds
  the app on YAML change while keeping the tcell handle (lines
  209-250).
- `view/keyboard_widget.go` — per-widget `charMap` / `keyMap` with
  `charHelp` / `keyHelp` slices used by the help modal (lines 22-168).
- `cfg/default_color_theme.go` — `ColorTheme` with
  `BorderTheme` / `RowTheme` / `TextTheme` / `WidgetTheme` and a
  per-widget override path (lines 45-91 + `cfg/common_settings.go:55-119`).
- `app/widget_maker.go` — module factory: giant switch over module
  type → `NewSettingsFromYAML` + `NewWidget` (lines 93-391).

**Patterns we're borrowing** — see D1, D2, C2, C4, E2.

**Skip**
- The 80+ provider widgets (Twitch, NBA scores, crypto…) — not
  relevant; a10r is single-domain.
- `tview.Pages` modal system — bubbletea modals are already in use
  and more flexible.
- The widget factory dispatch pattern — overkill for a10r's two
  backends today; revisit only if a third backend kind lands.
- Logger module that tails files — a10r already logs to its own
  file via `internal/log`.
- The tcell layer underneath — bubbletea/lipgloss is the right
  abstraction; nothing to lift.

### `yorukot/superfile`

Modern bubbletea file manager. Most directly comparable to a10r's
stack (same libs). Lift cleanly: layout, sidebar, modal stack, theme
files, command bar, mouse wheel.

**Key files**
- `src/internal/model.go` — composition root: layout
  (`mainComponentsRender`, lines 158-175), responsive height
  (`setHeightValues`), modal priority chain
  (`updateRenderForOverlay`, lines 137-147), mouse routing (lines
  79-84,111).
- `src/internal/model_render.go` — `terminalSizeWarnRender` for the
  too-narrow fallback (lines 23-44).
- `src/internal/ui/sidebar/sidebar.go` — sections (home / pinned /
  disks), search, rename of pinned items (lines 14-57).
- `src/internal/ui/helpmenu/{render,data}.go` — the searchable help
  modal with category grouping (`render.go:13-94`, `data.go:12-30`).
- `src/internal/ui/prompt/{type,model}.go` — command bar with
  shell/SPF dual-mode, suggestions, success/error result pane
  (`type.go:17-38`, `model.go:66-89`).
- `src/internal/common/config_type.go` + `load_config.go` — TOML
  schema for theme + hotkeys with override-on-default loading
  (`config_type.go:4-63`, `load_config.go:26-53`).
- `src/superfile_config/theme/nord.toml` + `hotkeys.toml` — concrete
  examples of the user-overridable config files.
- `src/internal/ui/notify/{type,model}.go` — reusable confirm modal
  with `ConfirmActionType` enum (`model.go:14-57`).
- `src/internal/ui/processbar/process.go` — async task model with
  state machine (InOperation / Successful / Cancelled / Failed)
  (lines 16-61).
- `src/internal/ui/rendering/renderer.go` — section-aware truncation
  with `TruncateStyle` (Head / Tail / None) (lines 21-73).

**Patterns we're borrowing** — see B1, B7, E1, E3, E4, E5, C3.

**Skip**
- File operations (copy / move / compress / preview) — out of
  domain.
- Nerdfont icon system (`src/config/icon/icon.go`) — a10r uses
  semantic glyphs (severity), not file-type icons.
- Zoxide jump nav, exiftool metadata, syntax-highlight preview —
  all file-manager specific.

### `janosmiko/lfk`

Modern bubbletea Kubernetes navigator, yazi-inspired (Miller
columns), explicitly k9s-spirit successor. Architecturally the
closest twin to a10r in this scout: same libs (bubbletea, lipgloss,
cobra), same single-binary shape, same vim-keyboard-first
ergonomic. Notable: lfk *embeds* an Alertmanager monitoring overlay
(`@` key) directly inside its k8s navigator — independent
validation that "Alertmanager alerts surfaced inside a TUI" is a
wanted feature, and that a10r's per-tenant `Alertmanager` endpoint
shape mirrors the one lfk already ships.

**Key files**
- `internal/ui/colormode.go:13-18,32-49,55-60` — runtime dark/light
  listener: `EnableColorModeCmd` writes `CSI ?2031h` (subscribe) +
  `CSI ?996n` (query); `ParseColorModeMsg` matches `CSI ?997;1n` /
  `CSI ?997;2n` reports; `SetColorMode` swaps the active scheme
  atomically (C5).
- `cmd/themegen/main.go` + `internal/ui/colorschemes_gen.go`
  (~167 KB) — code-generator that ingests ghostty themes into
  460+ Go-embedded schemes consumed via `BuiltinSchemes()` (C6).
- `internal/ui/cluster_color.go:18-27` — `ClusterColorNames` (eight
  stable named colours); `internal/ui/cluster_color_overlay.go:38-78`
  — swatch picker overlay; `internal/ui/config_keybindings.go:99-101`
  — picker binding (C7).
- `internal/ui/search.go:24-49` — `DetectSearchMode`: `~` fuzzy /
  `\` literal / regex-meta auto-detect / substring fallback (F4).
  `containsRegexMeta` at 51-60 lists the trigger set.
- `internal/app/history.go:23-26,39-57,134` — three persistent
  history rings (`history`, `query-history`, `log-search-history`),
  plain text, 0o600, under `$XDG_STATE_HOME/lfk/`. Ring is shared
  only when the matcher is identical (G4 amendment).
- `internal/app/bookmarks.go:13-25` — bookmark persistence to
  `$XDG_STATE_HOME/lfk/bookmarks.yaml` with backup/migration
  fallbacks (E7).
- `internal/ui/config_keybindings.go:43-49,84-90,121-122,140-144`
  — sort cycle (`>`/`<`/`=`/`-`, B8), watch toggle (`w`, D4), tab
  management (`t`/`]`/`[`, E6), bookmark slots (`m`/`'`, E7),
  background-tasks overlay (``` ` ```, D5), column toggle (`,`,
  refines G2), monitoring overlay (`@`).
- `internal/ui/overlay_background_tasks.go:11-22,61+` — overlay
  with `ModeRunning` / `ModeCompleted` states, live elapsed-time
  rendering (D5).
- `internal/ui/overlay_columns.go:14-28,36` — runtime column
  show/hide/reorder overlay with `/`-prefix filter (refines G2:
  per-launch overlay alongside the persistent `views.yaml`).
- `internal/ui/hintbar.go:74-77` + `internal/ui/config.go:322-323`
  — `RenderHintBar`; `tips: false` opt-out (E8).
- `internal/ui/overlay_monitoring_misc.go:312-420` —
  `RenderAlertsOverlay` rendering Alertmanager alerts inside the
  k8s TUI; field set: Severity (critical/warning), State
  (firing/resolved), Name, Summary, Description, Since,
  GrafanaURL. The render pattern is what a10r already does on the
  alerts page; the *field set* is a useful sanity-check that we
  cover what a sister tool finds load-bearing.
- `internal/model/actions.go:455-471` — `MonitoringEndpoint`
  (`Namespaces`, `Services`, `Port`) and `MonitoringConfig` with
  `Prometheus` / `Alertmanager` fields, indexed per-cluster via
  `ConfigMonitoring` with a `_global` fallback key. The
  `_global`-key trick is worth lifting for a10r's per-tenant
  config defaults (`backends.<name>` overrides on top of a
  `defaults` block).
- `README.md:75-76` — recursive symlink-following config-dir
  discovery for `~/.kube/config.d/*` plus `KUBECONFIG` env merge
  (A9).

**Patterns we're borrowing** — A9 (config-dir merge), B8 (sort UX),
C5 (runtime dark/light), C6 (theme codegen), C7 (per-tenant
accent), D4 (watch toggle), D5 (bg-tasks overlay), E6 (tabs), E7
(bookmarks), E8 (hint bar), F4 (search-mode autodetect), and the
lfk amendment to G4 (per-matcher-class history files).

**Patterns a10r already mirrors**
- Multi-select with Space marks + bulk action fan-out — already
  shipped on the alerts page
  (`internal/tui/page/alerts/alerts.go:212-227`, action registry
  `Bulk` flag at `internal/tui/action/action.go:53`).
- Read-only mode with `[RO]` badge and runtime toggle — already
  shipped (`internal/tui/action/action.go:48-50`, header indicator
  in the chrome).
- Theme files with user override under `<config-dir>/skins/` —
  already shipped (`docs/design/theming.md`).
- Action-registry-driven help — already shipped.

**Skip**
- The 30+ k8s resource types, owner-resolution, ArgoCD / Helm /
  KEDA / External-Secrets integrations, RBAC `can-i` browser,
  kubeconfig context handling, namespace selector, Prometheus
  resource dashboard. All k8s-domain.
- PTY-mode embedded terminal (`Ctrl+T` cycle pty/exec/mux) — a10r
  already shells out to `$EDITOR` for silence comments via
  `tea.ExecProcess`; that's enough.
- Resource templates / 25+ canned manifests — for a10r the closer
  analogue is "silence templates" (covered under E4 saved queries
  and F1 silence comment templating).
- Helm/ArgoCD bulk sync, port-forward, drain, scale — k8s admin.
- Nerd-font icon system (`auto/unicode/nerdfont/simple/emoji/none`)
  — a10r uses semantic glyphs only; ASCII fallback is captured
  under B2.
- Miller-columns layout — explicit divergence: a10r mirrors k9s's
  page-stack, which is a better fit for Alertmanager's flat
  resource shape than yazi's parent/current/preview triptych.
- Multi-cluster session model around tabs (E6 takes the *tab*
  pattern; the k8s-cluster-binding around it is irrelevant).
- BoltDB-style local-state — not used here; YAML + plain-text
  rings are sufficient for a10r's persistence needs.

### `twpayne/chezmoi`

Mature CLI dotfile manager. Not a TUI but the most polished
non-TUI UX in the scout set. Lift wholesale for `doctor`, `init`,
config layering, command grouping, templating.

**Key files**
- `internal/cmd/config.go` — root command construction and persistent
  flags (`newRootCmd`, lines 1878-2018; persistent flags including
  `Color` / `Progress` as env-aware `autoBool`, lines 1890-1946);
  config layering / XDG search (lines 981-1083); env-var overrides
  (lines 2498-2544); slog setup (lines 265, 378, 2293-2301);
  template-func registration (lines 385-386, 493-1000).
- `internal/cmd/doctorcmd.go` — pluggable `check` interface returning
  one of `omitted/failed/skipped/ok/info/warning/error`; tabwriter
  rendering (lines 33-70).
- `internal/cmd/initcmd.go` — interactive init flow with
  `--guess-repo-url`, `--one-shot`, `--purge`; `promptString` /
  `promptChoice` template funcs registered only during init
  (lines 86-123 + `config.go:950-964`).
- `internal/cmd/diffcmd.go` — diff/dry-run pattern reusing
  `applyArgs()` with include/exclude filters (lines 9-62).
- `internal/cmd/main_test.go` — `testscript` + `txtar` end-to-end
  command tests with custom commands like `mkfile` / `chhome`
  (lines 50-174). Useful template for a10r's CLI tests.
- `internal/cmd/cmd.go` — `Main` exit-code translation,
  `deDuplicateError` for clean error output (lines 52-66).
- `internal/cmd/addcmd.go:49` — `Aliases: []string{"manage"}`
  pattern for command aliases.

**Patterns we're borrowing** — see A1, A3, A6, A7, F1, A5.

**Skip**
- Encryption (age / GPG / vault funcs) — token storage is a separate
  concern; let users use OS keyrings.
- Source-of-truth git workflow — alertmanager IS the source of truth
  for a10r; chezmoi's "managed file" model doesn't translate.
- Pre/post hooks running arbitrary scripts — not needed.
- The full add/edit/apply/destroy CRUD over filesystem — a10r CRUD
  is API objects, not files.
- BoltDB persistent state for managed-file tracking — overkill;
  YAML for saved queries (E4) is enough.

### `trufflesecurity/trufflehog`

Long-running multi-source scanner CLI. Most useful for the
"output strategy" pattern, redaction-aware logging, multi-target
concurrency model, and exit-code semantics.

**Key files**
- `pkg/output/{json,plain,github_actions}.go` — three concrete
  `Printer` implementations behind one interface.
- `pkg/engine/engine.go` — `Printer` interface + `PrinterDispatcher`
  adapter, decoupling output format from scan logic.
- `pkg/sources/job_progress.go` — `JobProgressHook`,
  `JobProgressMetrics` with `ElapsedTime`, `PercentComplete`,
  `TotalUnits`, `FinishedUnits`. Useful shape for any future
  long-running fan-out command.
- `main.go` — orchestration: `WithConcurrentSources` /
  `WithConcurrentUnits` / `WithBufferedOutput(64)` for backpressure;
  `--include-paths` (regex) + `--exclude-paths` (regex) +
  `--exclude-globs` (early glob filter); `--results
  verified,unverified,unknown` for output filtering; `os.Exit(183)`
  when results found and `--fail` set; final
  `logger.Info("finished scanning", chunks=…, …)` summary.
- `pkg/log/{log,redaction_core,dynamic_redactor}.go` — zap-based
  logger wrapped with a redaction core; secrets registered globally
  are redacted from log fields before write.
- `pkg/verificationcache/in_memory_metrics.go` — TTL cache of
  verification results with hit/miss metrics.
- `pkg/detectors/detectors.go` — `Result` struct (Verified bool,
  VerificationFromCache bool); optional `EndpointCustomizer`
  interface for per-detector endpoint overrides.
- `pkg/config/config.go` — protobuf-YAML schema-driven config
  parsing with strict unmarshaling (`Read`, `NewYAML`).

**Patterns we're borrowing** — see A2, A4, A5, A8, D3, F2, F3.

**Skip**
- The detector zoo (850+ secret detectors) — domain-specific, no
  cross-applicability.
- Git / GitLab / GitHub source engines — version control scanning
  isn't a10r's domain.
- The `analyzer` subcommand (permission analysis on found secrets)
  — secret-specific.
- Archive handlers (RPM / APK / TAR extraction) — irrelevant.
- Pre-receive hook and GitHub Actions output format — CI-specific
  surfaces.
- Protobuf for config schema — overkill; a10r's plain YAML +
  validation is sufficient. Keep the *strict-decode + line-precise
  errors* idea (A1, C3); drop the protobuf.

---

## Open questions to resolve before any of these land

- A1 `doctor`: do we expose per-check selection (`--only=auth`) or
  always run the full battery? Lean: full battery, with `--only`
  added when the list grows past ~10 checks.
- A2 output formatters: do `groups list` and `receivers list` need
  CSV in addition to json/yaml/table for spreadsheet workflows? Wait
  for a user signal.
- A3 `init`: should it offer to bootstrap a Mimir tenant config from
  a Grafana org URL (one fewer manual step)? Probably yes; gated
  behind a follow-up.
- B7 sidebar: collapses by default — but does the keybinding live on
  `Ctrl+B` (vim/k9s convention) or `Tab` (superfile convention)?
  Current preference: `Ctrl+B`, since `Tab` is reserved for
  multi-panel focus (E2) and group expand on the groups page.
- C2 hot-reload: scope to `theme` only initially (low risk), expand
  to backend list later? Yes.
- C3 user keybindings: how do we handle the `0-9` tenant quick
  switch when the user wants to rebind those? Probably reserve them
  as system bindings that user files can't override; document in
  the schema.
- G1 plugin system: do we exec plugins via `os/exec` directly, or
  proxy through `tea.ExecProcess` (which a10r already uses for
  the silence editor at `Ctrl+E`)? Lean: `tea.ExecProcess` for
  foreground plugins so the TUI suspends cleanly; bare `os/exec`
  with stdout/stderr captured for `Background: true` plugins. Mirror
  k9s's Background flag exactly.
- G1 plugin scoping: how do we identify the "current row context"
  passed to plugin args? Lean: the page model exports a
  `RowContext() map[string]any` interface; the templating engine
  from F1 evaluates against it. Same shape as k9s but typed.
- G2 views.yaml vs C4 per-page colour: both are user-side overrides
  on page rendering. Land G2 first (column choice is more
  load-bearing than colour); reuse the loader for C4 later.
- E6 in-session tabs vs page-stack: do tabs cycle *across* the page
  stack or *within* their own? Lean: each tab owns an independent
  stack (lfk pattern), so `]` keeps the destination tab's last
  page. The single-stack alternative defeats the point of tabs.
- E7 bookmark scope: lfk's lowercase=context-aware /
  uppercase=context-free split maps cleanly to a10r's tenant scope
  (lowercase restores tenant; uppercase doesn't). Confirm before
  binding `m` and `'`.
- C5 vs C1: when both fire (terminal supports CSI ?2031 *and* user
  set `theme: auto`), runtime listener wins and overrides the
  startup detect. Document explicitly so users debugging "wrong
  theme" don't chase ghosts.
- F4 vs F2: F4 covers the live `/` filter; F2's label-selector
  parser stays for `:silences list --filter` where matcher syntax
  is explicit. Open: is regex auto-detect on a single `.` too
  aggressive? Lean: yes — require at least one *other* regex meta
  before flipping to regex mode, so `team=foo.bar` stays
  substring.
- C6 theme codegen: vendor a ghostty-themes snapshot into the
  repo, or fetch on demand from a `make` target? Lean: vendor (no
  network at build time), refresh quarterly. License of input
  themes (Apache-2.0 ghostty + per-theme MIT/etc.) survives the
  conversion — keep `NOTICE`/`LICENSE` carried through the
  generator output.
- A9 config-dir merge: do duplicate `backends.<name>` entries
  fail-closed (precise error) or last-wins? Lean: fail-closed —
  matches how YAML schema validation already treats duplicates,
  and avoids silent overrides ops teams won't notice.

---

## What we are explicitly *not* taking from this scout

- Complex plugin / module ecosystems (wtf's 80+ widgets,
  trufflehog's detector zoo, k9s's full 30+ resource-type views).
  a10r is a focused tool; resist feature sprawl. The k9s *plugin
  surface* (G1) we are taking — it's the user-side extension
  point, not the in-binary widget zoo.
- Encryption / secret-store integration (chezmoi's age, vault).
  Defer to OS keyring conventions.
- Git-as-source-of-truth workflows (chezmoi, trufflehog). a10r's
  source of truth is the live alertmanager API.
- File-manager interaction patterns (superfile's preview pane,
  copy/move). Out of domain.
- Click-to-focus mouse interactions (superfile keeps these minimal
  too). Stay keyboard-first per a10r's k9s lineage.
