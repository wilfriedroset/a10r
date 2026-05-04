# a10r

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A modern, fast, intuitive TUI for [Prometheus Alertmanager][am] and
[Grafana Mimir][mimir], inspired by [k9s][k9s].

## Features

- **Alerts list** with vim motions, substring filter, sort-column
  walk, state-filter cycle, and a per-row severity column.
- **Silences list** sorted by `endsAt` ascending so soonest-
  expiring is at the top; full create / edit / expire bindings
  behind read-only mode.
- **Silence form** with multi-line matchers (`name=value`,
  `name=~regex`), RFC3339 / duration shorthand for time fields,
  and line-precise validation errors.
- **Status pane** showing cluster state, version info, and the
  raw `config.original` YAML with `c`/`v`/`p` anchor jumps.
- **Receivers** with Enter-to-drill into alerts filtered by
  receiver. **Alert groups** as a two-level tree with Tab to
  expand-all and `s` to silence-by-common-labels.
- **Tenant table** for multi-backend setups: `0`/`1`-`9` quick-
  switch, Space toggles, `a` selects all.
- **Help overlay** (`?`) auto-built from the active action
  registry — read-only mode hides every Dangerous binding.
- **Bracketed paste** in `:` and `/` prompts, multi-byte rune
  handling on backspace, fuzzy-matched tenant picker.
- **External editor** handoff (`Ctrl+E` on a silence) via
  `tea.ExecProcess` honouring `$A10R_EDITOR` / `$EDITOR`.
- **Three bundled themes**: catppuccin-mocha (default),
  catppuccin-latte, gruvbox-dark. User skins shadow bundled by
  basename under `<config-dir>/skins/`.
- **Two backends**: vanilla Alertmanager v2 (floor v0.28.1) and
  Grafana Mimir (v2.17+) via prefix + tenant header. Multi-tenant
  fan-out with bounded goroutine pool.

## Install

```sh
go install github.com/wilfriedroset/a10r@v0.1.0
```

Binary releases land on the [GitHub release page][rel] once the
v0.1.0 tag is pushed: Linux (amd64, arm64, armv7), FreeBSD (amd64,
arm64), Windows (amd64, arm64), and a single Darwin universal
tarball (amd64+arm64 merged), plus deb / rpm / apk packages for
linux amd64 / arm64 / arm.

## Quickstart

```sh
# 1. Run a10r — the first invocation prompts you for a backend
#    URL when no config exists.
a10r

# 2. Or pre-create a config:
mkdir -p ~/.config/a10r
cat > ~/.config/a10r/a10r.yaml <<EOF
backends:
  - name: prod
    url: https://alertmanager.example
EOF
a10r
```

Pass `-c <path>` (or `--config <path>`) to point at an explicit
config file instead of the XDG-resolved directory:

```sh
a10r -c examples/demo.yaml
```

`a10r validate <path>` exits 0 when the config parses cleanly,
with a line-precise error message otherwise.

`a10r info` prints the resolved config dir, log path, and the
backend list with capability flags.

## Keybindings

| Key | Action |
| --- | --- |
| `?` | Help overlay (auto-built from the active page's bindings) |
| `:` | Command bar (`:alerts`, `:silences`, `:sil`, `:status`, `:q`) |
| `/` | Filter prompt — substring match across labels and annotations |
| `j`/`k` or `Down`/`Up` | Cursor walk |
| `gg` / `G` | First / last row |
| `Ctrl+D` / `Ctrl+U` | Half page down / up |
| `Ctrl+F` / `Ctrl+B` | Full page down / up (vim siblings of `Ctrl+D` / `Ctrl+U`) |
| `Space` | Mark / unmark a row (multi-select) |
| `s` | Silence (alerts page; Dangerous, hidden in read-only) |
| `t` | Cycle state filter (alerts page) |
| `Tab` | Expand / collapse all (groups page) |
| `Ctrl+T` | Tenant picker (multi-tenant deployments) |
| `0` | Scope: every configured tenant |
| `1` … `9` | Scope: nth tenant in `backends:` order |
| `Esc` | Dismiss modal / prompt; pop the page stack |
| `q` / `Ctrl+C` | Quit |

End-user cheat-sheet (per view) in [`docs/end-users/keybindings.md`](docs/end-users/keybindings.md). Full catalogue with the precedence stack and reserved keys in [`docs/design/keybindings.md`](docs/design/keybindings.md).

## Configuration

See [`docs/end-users/configuration.md`](docs/end-users/configuration.md) for the full schema.

A minimal vanilla Alertmanager config:

```yaml
backends:
  - name: prod
    url: https://alertmanager.example
```

A Mimir config with one tenant. The schema mirrors Prometheus's
[`remote_write`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write)
block — paste a `remote_write` entry, change the `url:` path, and
you are done:

```yaml
backends:
  - name: mimir-prod
    url: https://mimir.example
    prefix: /api/prom
    bearer_token: ${MIMIR_TOKEN}
    headers:
      X-Scope-OrgID: tenant-1
```

Environment variables in any string field are expanded via
`${VAR}` and `${VAR:-default}`.

## Documentation

- [`docs/end-users/quickstart.md`](docs/end-users/quickstart.md) — the 60-second tour.
- [`docs/end-users/keybindings.md`](docs/end-users/keybindings.md) — printable cheat-sheet per view.
- [`docs/end-users/configuration.md`](docs/end-users/configuration.md) — config schema, every field documented.
- [`docs/end-users/troubleshooting.md`](docs/end-users/troubleshooting.md) — common problems, diagnostic flags.
- [`docs/design/`](docs/design/) — the design documents that drove the implementation. Useful for contributors.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). The TL;DR is:

- Tests first. Every code commit ships with tests for the happy
  path and meaningful edge cases.
- One commit per logical unit. No WIP history; no fix-ups in
  follow-ups.
- `prek -a` must pass; `golangci-lint run ./...` must be clean;
  `go test -race ./...` must be green.
- DCO sign-off required (`git commit -s`).

## License

Apache 2.0. See [LICENSE](LICENSE).

[am]: https://github.com/prometheus/alertmanager
[mimir]: https://github.com/grafana/mimir
[k9s]: https://github.com/derailed/k9s
[rel]: https://github.com/wilfriedroset/a10r/releases
