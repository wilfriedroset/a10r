# Changelog

All notable changes to a10r are documented in this file. The
format is based on [Keep a Changelog][kac]; the project adheres
to [Semantic Versioning][semver].

## [Unreleased]

### Added

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
- **Docker images** — multi-arch (amd64, arm64) distroless images
  pushed to `ghcr.io/wilfriedroset/a10r` by the release pipeline,
  plus a standalone build-from-source `Dockerfile`.

### Changed

- **The default theme is now `auto`** instead of
  `catppuccin-mocha`. A config file that names a skin is
  unaffected. To keep the old behaviour on a light terminal, set
  `theme.name: catppuccin-mocha`. The name `auto` is now reserved:
  a user skin cannot use it.

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
