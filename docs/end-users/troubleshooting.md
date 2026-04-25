# Troubleshooting

## Connection state shows `○ unreachable`

Check the resolved backend list and try a manual GET:

```sh
a10r info
curl -sv https://alertmanager.example/api/v2/status
```

Common causes:

- The URL points at the AM web UI, not its v2 API. Append
  `/api/v2` to the base URL (or `/api/prom` for Mimir).
- TLS verification fails. `--debug` surfaces the underlying
  error. v0.1 doesn't expose an `insecure_skip_verify` knob —
  install the CA in the system trust store instead.
- Network policy / firewall blocks egress.

## `:` command bar says "ambiguous: foo, foam"

Two registered aliases share `foo` as a prefix. Either type the
full name or pick a less-ambiguous prefix — the resolver lists
the candidates so you know which.

## Silences I created don't show up

The poll tick is what refreshes the silences page; default
interval is 1 minute. Press `r` to force an immediate refresh,
or set `defaults.poll_interval: 5s` in the config for
development.

## `Ctrl+S` does nothing on the alerts page

`Ctrl+S` is the XOFF terminal control. Modern terminals pass it
through, but if yours doesn't, run:

```sh
stty -ixon
```

in the parent shell before launching a10r. Alternatively use
`:silence` from the command bar, or open the alert via Enter and
press `s` on the detail page.

## The header strip says `○ unreachable` *while* alerts are visible

A connection-state transition lags one poll tick behind the data
because the poller emits a transition only when the state
actually changes. If you see stale data, it's the cached
snapshot from the last successful tick; the poller is in the
backoff window. Wait one cycle, or `r` to retry.

## Read-only mode hides bindings I expected to see

The `?` help overlay also hides Dangerous entries under
read-only mode — check that you didn't pass `--read-only` (or
that `defaults.read_only` isn't set in the config). `a10r info`
shows whether read-only is active.

## `$EDITOR` opens but my edits don't persist

The wizard / silence-editor flow respects `$A10R_EDITOR` first,
then `$EDITOR`, then falls back to `vi` (Linux/macOS) or
`notepad` (Windows). Make sure the editor doesn't background
itself — the program waits for the editor process to exit before
re-reading the file.

For graphical editors that fork by default, set the foreground
flag explicitly:

```sh
export EDITOR='code --wait'         # VS Code
export EDITOR='subl --wait'         # Sublime Text
```

## Logs are nowhere to be found

`a10r info` prints the resolved log path. By default it's under
`$XDG_STATE_HOME/a10r/` (Linux/macOS) or `%LOCALAPPDATA%\a10r\`
(Windows). Override with `--log <path>` or `log.path` in the
config.

`--debug` raises the level to debug for the current run; `--quiet`
drops it to warn.

## Wizard ran, but I want to re-run it

Delete or rename the existing config — the wizard refuses to
overwrite. After:

```sh
mv ~/.config/a10r/a10r.yaml ~/.config/a10r/a10r.yaml.bak
a10r
```

## `:tenant` quick-switch doesn't match my config order

The numeric quick-switch (`1`-`9`) maps to the order in the
`backends:` array. Reorder the array if you want a different
mnemonic. The tenant picker (`Ctrl+T`) shows the alphabetical
order to keep the visual list stable across config edits.
