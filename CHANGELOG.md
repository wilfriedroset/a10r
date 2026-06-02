# Changelog

All notable changes to a10r are documented in this file. The
format is based on [Keep a Changelog][kac]; the project adheres
to [Semantic Versioning][semver].

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
