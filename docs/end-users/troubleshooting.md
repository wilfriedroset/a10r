# Troubleshooting

## Connection state shows `○ unreachable`

Check the resolved backend list and try a manual GET:

```sh
a10r info
curl -sv https://alertmanager.example/api/v2/status   # client adds /api/v2 itself
```

Common causes:

- The URL is wrong. The `url:` field in the config is the
  Alertmanager *root* — a10r appends `/api/v2` itself. So a
  config that says `url: https://am.example/api/v2` actually
  hits `/api/v2/api/v2/alerts` and 404s. For Mimir, set the
  Alertmanager prefix via the `prefix:` field (e.g.
  `prefix: /alertmanager`) so the URL is still the root.
- TLS verification fails. `--debug` surfaces the underlying
  error. The fix is to trust the CA — install it in the system
  trust store, or point the backend's `tls_config.ca` at your CA
  bundle (see [configuration.md](configuration.md)). As a last
  resort `tls_config.insecure_skip_verify: true` disables
  verification for that backend; only set it knowingly, it defeats
  TLS authentication.
- Network policy / firewall blocks egress.
- The backend is not an Alertmanager. Prometheus, a Loki ruler,
  and vmalert evaluate rules and *notify* an Alertmanager — they
  do not serve the v2 API a10r reads, so `/api/v2/status` 404s.
  Point a10r at the Alertmanager they notify. See
  [topology.md](topology.md).

## `:` command bar says "ambiguous: foo, foam"

Two registered aliases share `foo` as a prefix. Either type the
full name or pick a less-ambiguous prefix — the resolver lists
the candidates so you know which.

## Silences I created don't show up

The poll tick is what refreshes the silences page; default
interval is 1 minute. Press `r` to force an immediate refresh,
or set `defaults.poll_interval: 5s` in the config for
development.

## The header strip says `○ unreachable` *while* alerts are visible

A connection-state transition lags one poll tick behind the data
because the poller emits a transition only when the state
actually changes. If you see stale data, it's the cached
snapshot from the last successful tick; the poller is in the
backoff window. Wait one cycle, or `r` to retry.

## Read-only mode hides bindings I expected to see

The `?` help overlay also hides Dangerous entries under
read-only mode — check that you didn't pass `--read-only` (or
that `defaults.read_only` isn't set in the config). A binding that
vanishes only on some rows is a per-backend `read_only: true` instead: `?` keeps it
with a `[guarded]` suffix and the press names the frozen backend.

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

`a10r info` prints the resolved log path. The default follows the
platform convention: `$XDG_STATE_HOME/a10r/a10r.log` on Linux,
`~/Library/Logs/a10r/a10r.log` on macOS, and
`%LOCALAPPDATA%\a10r\Logs\a10r.log` on Windows. Override with
`--log <path>` or `log.path` in the config.

`--debug` raises the level to debug for the current run; `--quiet`
drops it to warn.

Inside the TUI, `:config` shows what the last start warned about
without a trip to the log file. `--quiet` does not empty that list.

## Which file set this value?

`:config` lists every file a10r read, in the order the merge applied
them: the base config, each drop-in in lexical order, then the
aliases, keys, and skin overlays. Search that list for the file that
set a value you did not expect. Most scalar keys are last-key-wins, so
start from the last drop-in; see the merge rules in
[configuration.md](configuration.md) for the keys that are not. `p`
jumps to the source list and `w` to the warnings.

## Wizard ran, but I want to re-run it

Delete or rename the existing config — the wizard refuses to
overwrite. After:

```sh
mv ~/.config/a10r/a10r.yaml ~/.config/a10r/a10r.yaml.bak
a10r
```

## a10r opens on the wrong tenant

With `tui.remember: true`, a10r reopens on the tenant scope you last
selected. `a10r info` reports that scope and the state dir holding
`ui-state.yaml` (see [configuration](configuration.md)). Delete that file to forget the
scope, or set `tui.remember: false` to stop a10r remembering it at
all. The same file holds the remembered sort column for each page.

## `:tenant` quick-switch doesn't match my config order

The numeric quick-switch (`1`-`9`) maps to the order in the
`backends:` array. Reorder the array if you want a different
mnemonic. The tenant picker (`Ctrl+T`) shows the alphabetical
order to keep the visual list stable across config edits.

## No desktop notification arrives

With `tui.notify.enabled: true`, a10r writes an escape sequence and
the terminal turns it into a desktop notification. Two things stop the
sequence.

- A multiplexer sits between a10r and the terminal. tmux reads OSC 9
  and OSC 777 itself and passes neither one out, so the terminal never
  sees them. Treat every other multiplexer the same way until you
  prove otherwise.
- The terminal does not know the sequence you picked. The table in
  [configuration.md](configuration.md#notifications) records what each
  terminal documents.

Run a10r outside the multiplexer and wait for one new firing alert. A
notification that arrives there names the multiplexer as the cause.

The way out is `tui.notify.command`. a10r runs the program you name
and passes the message as one argument:

```yaml
tui:
  notify:
    enabled: true
    command: ["notify-send", "a10r", "$MESSAGE"]
```

a10r resolves `desktop` to `off` when you set `command` and leave
`desktop` unset, so the program runs alone. Name `desktop` yourself to
get both.

The bell travels in the same write as the escape sequence, but a
multiplexer passes a bell through to the terminal. The flash strip is
drawn by a10r itself. Both reach you wherever a10r runs. Keep
`bell: true` for an audible signal when no desktop notification is
possible.

## I pasted a password into a backend `url`

a10r removes the `user:password@` part before it prints a backend
URL. `a10r info`, the `:info` page, the `:tenant` table and the
tenant config inspector all show the scheme, the host, the port and
the path only. When the backend sets no `basic_auth`, no
`authorization` and no `bearer_token`, the `auth:` line then reads
`url userinfo`, so the report still tells you that the backend
authenticates.

The config file itself is not touched. Your password is still in it
in clear text. Give the file the same care as any other secret:
`chmod 600`, keep it out of version control, and prefer
`basic_auth:` with an interpolated environment variable.
