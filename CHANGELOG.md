# Changelog

All notable changes to a10r are documented in this file. The
format is based on [Keep a Changelog][kac]; the project adheres
to [Semantic Versioning][semver].

## [Unreleased]

## [v0.4.0] — 2026-10-11

### Added

- **In-app info, config, skin, and reload** — four `:` commands answer
  a config question without leaving the TUI. `:info` prints the same
  report `a10r info` prints. `:config` lists the files the load
  actually read and what startup warned about, with `p` and `w` as
  section anchors. `:skin` picks a skin for the session without a
  restart, and it never writes `theme.name`. `:reload` re-reads the
  config file, the aliases file and the keys file while your page
  stack, cursors, marks, filters and tenant scope stay as they are. A
  reload refuses the whole file when it changes a backend or the log.
  Everything else applies at once, including to the pages already on
  your stack: `read_only`, `guardrails`, `bulk_concurrency`, label
  columns, `tui.notify` and `tui.terminal_title`. The one exception is
  `tui.remember`, which the flash names for a restart. See
  `docs/end-users/keybindings.md`.

- **Alert notifications** — with `tui.notify.enabled: true`, a poll
  that brings a firing alert the poll before it did not have rings the
  terminal bell, writes a desktop notification, and flashes one line.
  The line reads `prod-eu: HighLatency (critical)`, or `prod-eu: 3 new
  firing alerts, worst critical` when the poll brings several. One
  poll raises one notification and one bell, whatever the number of
  new alerts. `desktop` picks the escape sequence (`osc777`, `osc9`,
  `both`, `off`) and `min_severity` sets the floor a group must reach;
  `info` notifies on every firing alert, one with no known severity too.
  `command` runs a program such as `notify-send` instead of, or beside,
  the escape sequence, and it runs without a shell: a10r replaces each
  `$MESSAGE` element of the argv with the text as one argument. The
  first poll of a backend only seeds the set and stays quiet, and so
  does a backend that returns to the tenant scope. Off by default.

- **Guardrails** — a `guardrails:` list in the config restricts write
  verbs per tenant. A rule can deny a verb, so the key stops appearing
  in the hint strip and a press flashes the reason instead of acting.
  A rule can cap how many targets one press sends to one backend with
  `max_bulk`, counted per tenant rather than per run. A rule can raise
  the confirmation a verb asks for to `type-tenant-name`, where you
  retype the backend name before the write goes out, once per
  restricted backend the run touches. Rules only tighten, never
  loosen: read-only is checked first and always wins, and a verb no
  rule names keeps the prompt it already had. A refusal names the
  press when it fans out over your marks, for example `bulk silence
  denied on prod-eu: use the change ticket`, and names the verb when
  the cursor row alone is the target. `a10r validate` refuses a rule
  that names an unknown verb, an unknown level, or a `max_bulk` below
  `1`. `a10r validate` (on stderr) and `a10r info` both list any rule
  whose tenants match no backend, and the TUI logs the same warnings at
  startup so `:config` shows them. `a10r info` also lists the rules in
  force. See ADR 0049.

- **Label columns** — add your own columns to the alerts list and the
  group detail page with a `columns:` list under `pages`, one entry
  per alert label. Each column takes an optional title, a fixed
  width, a `Shift+<letter>` sort key, and `wide: true` to park it
  behind the `Shift+W` tier. An alerts row is an alertname aggregate,
  so a cell that disagrees across instances reads `<N values>` rather
  than picking one. When the row outgrows the terminal, both pages
  drop columns off the right edge behind a `>` marker and `←` / `→`
  scroll through them, with the first column pinned. See ADR 0048.

- **Remembered scope and sort** — with `tui.remember: true`, a10r
  reopens on the tenant scope you last selected and on the sort
  column and direction you last picked for each list page. The values
  live in `ui-state.yaml` in the state dir, next to the prompt
  history. A remembered tenant that is no longer in the config is
  dropped with a warning and the scope falls back to every backend.
  a10r writes the pruned scope back, so the warning appears once. A
  page back on its built-in sort drops out of the file, so what stays
  on disk is short enough to read and edit by hand. The file survives
  a power cut, like the prompt history. `a10r info` now reports the
  state dir and the remembered scope. Off by default.
- **Poll delta flash** — with `tui.poll_delta: true`, a poll that adds
  or removes an alertname in the alerts list flashes one line, for
  example `+3 new, -1 resolved`. A poll that changes nothing stays
  quiet, and so does the first poll of each backend. The line names
  the tenant when the scope spans more than one backend. The delta
  reports what the backend did, so the `/` filter and the state filter
  do not hide it. For one second after you press a key, the delta
  stays quiet rather than overwrite the feedback for that key. Off by
  default.
- **Automatic light and dark skin** — `theme.name: auto` asks the
  terminal for its background colour at startup and picks
  `catppuccin-latte` on a light terminal, `catppuccin-mocha` on a
  dark one. A terminal that does not answer keeps
  `catppuccin-mocha`. Detection runs once and never overrides a
  skin you named yourself. `a10r info` now reports the configured
  theme.
- **Terminal title** — with `tui.terminal_title: true`, a10r names the
  terminal window or tab `a10r: <scope> <page>`, for example
  `a10r: prod+2 alerts`, and updates it when you change page or tenant
  scope. Read-only runs add a `[read-only]` badge. The title is off by
  default, and a10r clears the title again when it exits.
- **Range marking** — `Shift+V` anchors on the cursor row and previews
  every row between the anchor and the cursor as marked. `Space` or a
  second `Shift+V` commits the preview, `Esc` cancels it, and `Ctrl+\`
  cancels it and clears every mark. A commit only adds, so marks you
  picked one by one survive it. The anchor is a row key rather than an
  index, so a re-sort carries the preview with its row. Available on
  the alerts list, the group detail page and the silences list.
- **Copy any field** — `Y` on the alert detail and the silence detail
  pages opens a field picker and copies the chosen field at full
  length over OSC52, so a truncated cell is no longer the only thing
  you can take out of the TUI.
- **Filter expressions** — the `/` prompt on the alerts list and the
  group detail page now reads `&&`, `||`, `!` and parentheses over the
  existing modes, plus the typed keys `count`, `age` and `state` with
  the six comparison operators. A buffer becomes an expression only
  when it carries a `||`, a `!`, a typed comparison, or a `(` next to
  an explicit `&&` or `,`. Every other buffer keeps the meaning it
  always had, so `a=1,b=2` stays the label-matcher AND chain and
  `(web|api)` stays a regex alternation. An expression paints no match
  highlight, because its matching characters spread across terms the
  highlighter cannot attribute. A dangling operator shows
  `[expr: <reason>]` in the title tag and leaves the rows on the last
  good filter. See
  `docs/end-users/keybindings.md#boolean-expressions`.
- **Filter match highlighting** — the characters that made a row
  survive the `/` filter are painted in the skin filter colour on the
  alerts, silences and receivers lists. Substring, literal, fuzzy and
  regex modes all paint. The cursor row, a marked row and a dimmed
  row underline the match instead, so the row keeps its own
  colour. A label matcher paints nothing: it matches on label
  structure rather than on the rendered text.
- **Invalid filter feedback** — a `/` buffer whose regex does not
  compile now shows the reason in the title tag and leaves the rows
  alone, instead of silently searching for the literal text. Enter
  keeps the prompt open; Esc restores the previous filter. The same
  rule covers the label-matcher form (`cluster_id=~9.*`) and the
  `:alerts --filter` value, which now refuses a pattern it cannot
  compile.

### Changed

- **The default theme is now `auto`** instead of
  `catppuccin-mocha`. A config file that names a skin is
  unaffected. To keep the old behaviour on a light terminal, set
  `theme.name: catppuccin-mocha`. The name `auto` is now reserved:
  a user skin cannot use it.
- **Per-backend `read_only` is now enforced per row.** A per-backend
  `read_only: true` short of a fully read-only session no longer hides
  the write keys everywhere. The keys stay up while any configured
  backend is writable, the hint strip drops the key on a frozen row,
  `?` keeps it with a `[guarded]` suffix, and a press refuses and
  names the backend.
- **A sideways scroll no longer outlives the columns it was made
  against.** `Shift+W`, or a scope change that adds or drops the
  TENANT column, now returns a horizontally scrolled row to the
  pinned left edge. Keeping the old offset opened the new column set
  parked at its far right, hiding the column the key was pressed to
  reveal.
- **Go modules** — bubbletea moves to v2.0.10, with ultraviolet,
  `golang.org/x/sync` and `golang.org/x/sys` bumped to their latest
  releases. No user-facing change.

### Fixed

- **A dry run of a read-only plan now exits like the real run.**
  `--dry-run` promises the exit code the real run's pre-mutation phase
  would give, so a clean dry run is a pre-commit gate. A plan against
  a read-only backend exited `0` where the apply exits `1`, so a
  wrapper could green-light a plan no backend accepts. The plan line
  now reads `[read-only: apply would be refused]`, and the dry run
  exits `1`. A plan that also holds a rule refusal exits `1` as well,
  so read-only never arrives as the guardrail code `6`.

- **A password inside a backend `url` no longer reaches the screen.**
  `a10r info` and the `:tenant` table strip the `user:password@` part
  of a backend URL before printing it. When the backend sets no other
  auth field, the `a10r info` `auth:` line reads `url userinfo`, so
  the report still says that the backend authenticates.

- **The prompt history now survives a power cut.** a10r writes the
  history files to a temp file and renames it into place. The rename
  alone protects the file against a crash of a10r, but not against a
  crash of the host: the new name could reach the disk before the
  bytes it points at. a10r now flushes the temp file and the directory
  entry before it reports the write as done.

- **Most text a backend sends can no longer repaint your terminal.**
  a10r
  replaces every control character with a space at the point where a
  wire response becomes a domain value. This covers labels,
  annotations, generator URLs, receiver names, cluster peers and the
  version block. An alert whose label carries an ANSI escape sequence
  therefore renders as text in every list, every detail page and
  every notification. Some values stay as sent. The backend
  configuration the `:status` page shows is a document rather than a
  cell. Alert fingerprints and silence IDs address the API, so a
  substitution there would break the lookups that use them. Silence
  authors, comments and matchers are written back by `silences
  update`, `silences recreate` and the editor flow, so they stay as
  sent and are cleaned where a10r draws them instead. `json` and
  `yaml` output carries them unchanged. One known gap remains: the
  TUI edit and recreate form still flattens comment newlines and
  splits or drops control characters in matchers.

### Security

- **Built with Go 1.27.2.** Go 1.27.1 has eight standard library
  vulnerabilities that the backend HTTP and TLS transport can reach:
  GO-2026-6603, GO-2026-6605, GO-2026-6607, GO-2026-6608,
  GO-2026-6610, GO-2026-6611, GO-2026-6613 and GO-2026-6617. Go
  1.27.2 fixes all of them.

## [v0.3.0] — 2026-09-06

### Changed

- **Go 1.27.1** — the build toolchain moves to Go 1.27.1, with the
  Charm stack, testify and the CI actions bumped to their latest
  releases. No user-facing change.

## [v0.2.0] — 2026-06-22

### Added

- **Silence lifecycle from the CLI** — `alerts get`, `silences get`,
  `silences create`, `silences update`, `silences expire` and
  `silences recreate` bring the headless CLI level with the TUI.
  `--tenant` now scopes every headless command. Writes fail closed: a
  read-only target aborts the command before anything is written.
  `alerts list` shows a FINGERPRINT column and `silences list` an ID
  column. A new exit code `5` means that no backend in scope knows
  the named alert or silence. `--output` gains the `-o` shorthand.
- **Output for coding agents** — a10r detects a coding agent from its
  environment, and `A10R_OUTPUT` sets the default format. An agent
  gets `json` by default, failures as an `{error,code}` envelope on
  stderr, and the follow-up command after a successful write.
  `--dry-run` on the silence write verbs prints the plan without a
  write, and exits with the code the real run gives before its first
  write. See ADR 0045
  and ADR 0046.
- **`a10r skills add`** — installs the embedded agent skill into
  `~/.agents/skills`, or `~/.claude/skills` with `--claude`, or any
  directory with `--dest`. `--dry-run` names the target without a
  write, and `--force` overwrites an older copy. `a10r skills preview`
  prints the skill.
- **`:quit`** — a synonym for `:q`.
- **Docker images** — multi-arch (amd64, arm64) distroless images
  pushed to `ghcr.io/wilfriedroset/a10r` by the release pipeline,
  plus a standalone build-from-source `Dockerfile`.

### Removed

- **The route-grouped groups page and `a10r groups list`.** The
  alerts page already aggregates by alertname, so the page added
  nothing. See ADR 0047.

### Fixed

- **`a10r validate` now refuses an unusable backend.** A backend with
  an empty name, a duplicate name or an empty `url` passed validation
  and failed later in every command.

- **`alerts list --state` now refuses an unknown state.** A typo
  matched nothing, so `--fail` never fired.

- **The alert detail page no longer hangs in a very narrow
  terminal.** A wrap width below the label indent looped forever.

## [v0.1.0] — 2026-06-03

First public release. a10r is a terminal UI for Prometheus
Alertmanager and Grafana Mimir, shaped like k9s — vim motions,
single-key actions, multi-tenant fan-out. The entry below mirrors
the shape of the shipped history, one bullet per area.

- **Project bootstrap** — repo scaffolding, Apache 2.0 license,
  Makefile, `go.mod`, prek hooks, golangci-lint config, and the
  CI / fuzz / release (goreleaser + build-provenance attestations)
  pipelines.
- **Config loader** — YAML schema with XDG resolution, env-var
  interpolation (`${VAR}`, `${VAR:-default}`), CLI / env / file
  precedence, `config.d/` drop-in merge, and `info` / `validate`
  subcommands.
- **Vanilla Alertmanager v2 client** — the `Client` interface,
  sentinel errors, the vanilla read and write paths (alerts,
  silences, status, receivers, groups), capability flags, a
  retry policy, and a cap on the response body the decoder
  reads. The client speaks through an injected round-tripper;
  the hardened transport stack itself ships with the security
  work below.
- **Mimir + multi-tenant fan-out** — Mimir wrapper (path prefix +
  tenant header), backend factory, and multi-tenant fan-out over
  a bounded goroutine pool with per-tenant error propagation.
- **TUI shell** — Bubble Tea v2 header / body / footer frame,
  page stack with push / pop / replace, an action registry with a
  read-only filter, a key-precedence stack with chord timeout, and
  the palette + roles theme loader.
- **Pages** — alerts list and detail, silences list and form,
  status pane with raw config viewer, receivers (drill to
  alerts), alert groups (two-level tree), and the tenant table,
  built on shared list-page and detail-page bases; silence
  writes are audit-logged.
- **Polish, command surface, and doctor** — tenant picker and
  confirm modals, command bar with alias resolver, footer
  (crumbs, prompt, flash, history rings, rotating hints), help
  overlay, bracketed paste in prompts, watch-mode toggles, init
  wizard, `list` subcommands with json / yaml / table output,
  and the `doctor` preflight checks.
- **Bundled skins** — eight catppuccin skins (each with a
  `-transparent` sibling) using a k9s drop-in schema; user skins
  under `<config-dir>/skins/` shadow bundled ones by basename.
- **Security hardening** — the hardened HTTP transport stack the
  backend clients compose: host-pinned auth and header
  round-trippers, cross-origin redirect refusal, TLS warnings,
  and secret redaction in debug logs — plus a tempfile editor
  handoff.
- **Launch scaffolding** — code of conduct, security policy,
  issue and PR templates, dependabot, the govulncheck / CodeQL /
  stale workflows, the ADRs, CONTEXT.md, ARCHITECTURE.md,
  AGENTS.md, and the end-user and contributor docs.

[kac]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/
