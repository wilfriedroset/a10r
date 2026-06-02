# Quickstart

The 60-second tour.

## Install

```sh
go install github.com/wilfriedroset/a10r@v0.1.0
```

Or grab a pre-built binary from the [release page][rel].

## Configure

a10r resolves its config from (first match wins):

1. `--config <file>` / `-c <file>` — an explicit config file.
2. The directory passed via `--config-dir` (holds `a10r.yaml`).
3. `$A10R_CONFIG_DIR`.
4. `$XDG_CONFIG_HOME/a10r/` (default `~/.config/a10r/` on
   Linux/macOS, `%APPDATA%\a10r\` on Windows).

A minimal vanilla Alertmanager config:

```yaml
backends:
  - name: prod
    url: https://alertmanager.example
```

If no config exists, the first run prompts you through a wizard
that captures the URL, optional Mimir prefix, optional tenant
header, and auth type, then writes the resolved YAML.

## Launch

```sh
a10r
```

You land on the alerts list. The header strip shows the active
tenant, connection state, alert count, and time-since-last-poll.
The footer hosts breadcrumbs / prompt / flash messages.

## The keys you actually need

| Key | What |
| --- | --- |
| `?` | Help overlay listing every active binding. Start here. |
| `:` | Command bar — type `:silences`, `:status`, etc. |
| `/` | Filter prompt — substring match across labels and annotations. |
| `j` / `k` | Move the cursor down / up. |
| `Enter` | Drill into the cursor row. |
| `Space` | Mark the cursor row (multi-select). |
| `s` | Silence the alert at the cursor (skipped in `--read-only`). |
| `0` | Scope to every configured tenant. |
| `1` … `9` | Scope to the nth tenant in `backends:` order. |
| `Esc` | Dismiss the prompt or pop the page stack. |
| `Ctrl+C` | Hard quit. |

The full cheat-sheet (per view) lives in [`keybindings.md`](keybindings.md).

## Read-only mode

Pass `--read-only` (or set `read_only: true` in the config) to
hide every Dangerous binding. Useful when you're sharing a
terminal session, screensharing a triage call, or just don't
want a stray `s` to silence something by accident.

## Troubleshooting

If a tenant column shows `○` (unreachable):

1. `a10r info` to confirm the resolved backend list.
2. `a10r validate -c <path>` to confirm the config parses.
3. `--debug` flag for verbose log output (writes to the path
   shown by `a10r info`).

See [`troubleshooting.md`](troubleshooting.md) for more.

[rel]: https://github.com/wilfriedroset/a10r/releases
