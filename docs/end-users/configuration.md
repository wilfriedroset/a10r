# Configuration

a10r reads a single YAML file. Every string field is environment-
interpolated via `${VAR}` and `${VAR:-default}` so credentials
need not live in the file.

## File location

Resolution order (first match wins):

1. `--config <file>` / `-c <file>` (CLI flag) — an explicit file
   path; overrides `--config-dir` and the directory search below.
2. `--config-dir <path>` (CLI flag).
3. `$A10R_CONFIG_DIR`.
4. `$XDG_CONFIG_HOME/a10r/` (Linux/macOS) or `%APPDATA%\a10r\`
   (Windows). Default `~/.config/a10r/`.

For options 2-4 the file inside the resolved directory is
`a10r.yaml`; `--config` / `-c` names the file directly.

### State files

a10r remembers a little between runs. Those files live in the state
dir, not the config dir: `$XDG_STATE_HOME/a10r/` when that variable is
set, else `~/.local/state/a10r/` on every platform. `a10r info` prints
the resolved path. The log file is the exception and follows the
platform convention instead, so on macOS and Windows it sits
elsewhere.

| File | Holds |
|---|---|
| `cmd-history` | recent `:` commands |
| `filter-history` | recent `/` filters |
| `silence-matcher-history` | recent silence-page matchers |
| `ui-state.yaml` | last tenant scope and per-page sort column, only when `tui.remember: true` |

Every one of them is optional. Delete any of them to start fresh;
a10r writes them again as you work. A `ui-state.yaml` that does not
parse turns the memory off for that run, and a10r leaves the file
alone so you can fix it.

## Schema

A backend is an Alertmanager v2 endpoint — vanilla Alertmanager or
Mimir. If you mean to point a10r at Prometheus, a Loki ruler, or
vmalert, see [topology.md](topology.md): those evaluate rules and
notify an Alertmanager, they do not serve the API a10r reads.

The `backends:` block uses the same shape as Prometheus's
[`remote_write`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write) —
copy a `remote_write` entry out of `prometheus.yml`, change the
`url:` to your Alertmanager v2 base, and you are done.

```yaml
backends:
  - name: prod                     # required, identifier
    url: https://alertmanager.example   # required, base URL
    prefix: /alertmanager          # optional, prepended to every path (Mimir)
    tenant_header: X-Scope-OrgID   # optional, header name (Mimir sugar; see headers below)
    tenant: tenant-1               # optional, value sent under tenant_header
    capabilities:                  # optional, opt-in beyond AM v2
      config_api: false
      tenant_admin: false
      ring: false

    # at most ONE of the three auth blocks may be set per backend
    basic_auth:
      username: alice
      password: ${AM_PASSWORD}
    # — or —
    authorization:
      type: Bearer                 # default; any wire scheme works (Token, GenieKey, …)
      credentials: ${AM_TOKEN}
    # — or —
    bearer_token: ${AM_TOKEN}      # shorthand for authorization: { type: Bearer, credentials: … }

    headers:                       # optional, free-form per-request headers
      X-Trace-Id: a10r
    tls_config:                    # optional, inline-only (file paths reserved for the F2 mTLS work)
      ca: |
        -----BEGIN CERTIFICATE-----
        # your internal CA bundle
        -----END CERTIFICATE-----
      server_name: alertmanager.internal
      insecure_skip_verify: false
      min_version: TLS12           # TLS10 | TLS11 | TLS12 | TLS13
      max_version: TLS13
    proxy_url: http://proxy:3128   # optional, route through HTTP proxy
    no_proxy: 127.0.0.1,localhost,.svc.cluster.local
    proxy_from_environment: false  # exclusive with proxy_url / no_proxy
    remote_timeout: 30s            # per-request timeout

    read_only: false               # optional, force read-only for this backend
    poll_interval: 30s             # optional, override defaults.poll_interval
defaults:
  poll_interval: 1m                # default for every backend
  read_only: false                 # default; --read-only flag still wins
  log_format: logfmt               # logfmt or json
theme:
  name: auto                       # auto, bundled, or under <config-dir>/skins/
log:
  path: /var/log/a10r.log          # default: $XDG_STATE_HOME/a10r/a10r.log
  level: info                      # debug, info, warn, error
tui:
  tips: false                      # optional rotating one-line hint bar (off by default)
  tips_interval: 8s                # optional cadence; falls back to 8s when omitted
  terminal_title: false            # optional terminal window title (off by default)
  poll_delta: false                # optional flash of what each poll changed (off by default)
  remember: false                  # optional memory of the last scope and sort column (off by default)
  notify:                          # optional alert notifications (off by default)
    enabled: false                 # ring and notify on a new firing alert
    bell: true                     # terminal bell, one per poll
    desktop: osc777                # osc777 | osc9 | both | off
    min_severity: warning          # critical | warning | info
    command: []                    # optional argv; "$MESSAGE" is one whole argument
keys:                              # optional rebindings (empty = use defaults)
guardrails:                        # optional write policy, see "Guardrails" below
  - tenants: ["prod-*"]
    actions: ["silence.expire"]
    deny: true
    reason: use the change ticket
```

## Authentication

Three peer blocks; at most one per backend (Prometheus's
`validateAuthConfigs` rule):

```yaml
# No auth — omit every block.

# HTTP Basic.
basic_auth:
  username: alice
  password: ${AM_PASSWORD}

# Generic Authorization header (any scheme: Bearer, Token, GenieKey, …).
authorization:
  type: Bearer
  credentials: ${AM_TOKEN}

# Shorthand for `authorization: { type: Bearer, credentials: … }`.
bearer_token: ${AM_TOKEN}
```

For gateway-style "send a custom header" auth, use the free-form
`headers:` map instead — there is no dedicated single-header auth
block.

```yaml
headers:
  X-Auth-Token: ${AM_TOKEN}
```

`Authorization`, `Host`, `Content-Type`, `Content-Length`, and
`Content-Encoding` are reserved and rejected at load time —
authentication must go through one of the auth blocks above.

`*_file` and `*_ref` keys (Prometheus's k8s-secret-mount and
secret-manager variants) are not supported. a10r's credential
sourcing is `${VAR}` interpolation; any field accepts an env-var
reference. mTLS and SigV4 are deferred to future releases.

## Multi-backend

```yaml
backends:
  - name: prod
    url: https://am-prod.example
  - name: staging
    url: https://am-staging.example
  - name: dev
    url: https://am-dev.example
```

`Ctrl+T` opens the tenant picker; `0` selects every configured
tenant; `1`/`2`/`3` quick-switch to the Nth. When more than one
tenant is selected, the alerts and silences tables surface a
synthetic `tenant` column so you know which backend each row
came from.

## Per-page poll intervals

The poll interval is resolved in priority order: `pages.<page>.poll_interval`
> per-backend `poll_interval` > `defaults.poll_interval` > 1 minute. A
non-zero per-page value wins for that page only — the same backend's
other pages keep their backend-derived defaults.

```yaml
defaults:
  poll_interval: 30s

backends:
  - name: prod
    url: https://am-prod.example
    poll_interval: 60s     # backend-wide override

pages:
  alerts:
    poll_interval: 5s      # alerts page polls every 5s
  silences:
    poll_interval: 30s
  status:
    poll_interval: 5m
```

Recognised page names: `alerts`, `silences`, `receivers`,
`status`. Omitted pages keep their backend-derived default.

### Label columns

The alerts page and the group-detail page render a fixed set of
columns. Add more with a `columns:` list, one entry per alert label.
The group-detail page is named `group_detail` and takes columns only,
because it rides the alerts poll feed and has no interval of its own.

```yaml
pages:
  alerts:
    columns:
      - label: cluster
        title: CLUSTER     # optional, default is the upper-cased label
        sort_key: L        # optional, binds Shift+L to sort by this column
        width: 12          # optional, fixed cells; default measures the view
        wide: true         # optional, hides the column until you press Shift+W
  group_detail:
    columns:
      - label: pod
```

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `label` | string | required | The alert label to read the cell from. |
| `title` | string | `label` | The header text. a10r upper-cases it. |
| `sort_key` | string | none | One uppercase letter. Binds `Shift+<letter>`. |
| `width` | int | measured | Fixed cell count. Minimum 3. |
| `wide` | bool | `false` | Hide the column behind the `Shift+W` tier. |

User columns render after ALERTNAME on the alerts page, and after
INSTANCE on the group-detail page, in the order you list them. You
cannot remove or reorder the built-in columns.

Every column you add makes the row wider. When the row no longer fits
the terminal, the page drops columns off the right edge instead of
squeezing all of them, and the header marks the cut with `>`. Press
`→` to scroll to the columns out of view and `←` to come back. The
first data column stays pinned so the row keeps its identity.

A column with `wide: true` stays out of view until you press
`Shift+W`, which toggles the wide tier for the page you are on. The
tier is per page and lasts for as long as that page stays open. If
you sort by a wide column and then leave the tier, the page falls
back to its default sort and direction, then gives your choice back
when you return. If you sort again while the wide column is out of
view, that new sort replaces the parked one.
A sort survives a page re-entry, but the tier does not, so a
remembered sort on a wide column starts parked.

An alerts row is an alertname aggregate, so several instances share
one cell. The cell shows the value when every instance agrees. When
they disagree, it shows `<N values>`, where N is the number of
distinct values. An instance with no such label counts as one
distinct value.

A group-detail row is a single instance, so its cell is the raw label
value or empty. There is no rollup marker on that page.

a10r rejects the configuration at startup when a column has an empty
or space-padded `label`, a duplicate `label` on one page, a `width`
below 3, a `sort_key` that is not one uppercase letter, a `sort_key`
or `title` another column already uses, or a `sort_key` or `title`
that a built-in already uses. The letters `A C F G N S T V W` are
taken on both pages. `G` is the jump-to-bottom motion, and the cursor
answers it before any sort does. Only `alerts` and `group_detail`
accept `columns`.

## Notifications

a10r can ring the terminal bell and raise a desktop notification when
a poll brings a firing alert that the poll before it did not have.
The feature is off. Set `tui.notify.enabled: true` to turn it on.

```yaml
tui:
  notify:
    enabled: true
    bell: true
    desktop: osc777
    min_severity: warning
    command: ["notify-send", "a10r", "$MESSAGE"]
```

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `enabled` | bool | `false` | Turn the whole feature on. |
| `bell` | bool | `true` | Ring the terminal bell. One ring per poll, whatever the number of new alerts. |
| `desktop` | string | `osc777`, or `off` when `command` is set | The escape sequence a10r writes. One of `osc777`, `osc9`, `both`, `off`. |
| `min_severity` | string | `warning` | The floor a group must reach to notify. One of `critical`, `warning`, `info`. |
| `command` | list of strings | empty | Argv of a program to run instead of, or beside, the escape sequence. The first element names the program, so it must be neither empty nor `$MESSAGE`. |

a10r rejects the configuration at startup when `desktop` is not one
of the four names, when `min_severity` is not one of the three
severities, or when the first element of `command` is empty or is
`$MESSAGE`.

a10r ranks three severities: `critical`, `warning` and `info`. An
alert with no `severity` label, or with a value that is none of the
three, stays below every floor and never notifies. No value of
`min_severity` notifies on everything.

The notification also writes one line to the flash strip, so the
in-app signal reaches you when every transport is off. The strip holds
one line at a time. With `tui.poll_delta` on as well, a poll that
brings a new alert raises two lines and you see only one of them, and
which one is not fixed. Turn `tui.poll_delta` off when you want the
notification line every time.

`command` runs the program directly. There is no shell, so there is
no word splitting and no variable expansion. a10r replaces each
element that is exactly `$MESSAGE` with the notification text as one
argument. Quote nothing yourself. Write the element as `$MESSAGE` and
never as `${MESSAGE}`: a10r expands `${NAME}` from the environment
while it reads the file, and it stops at startup when the variable is
unset.

`command` and `desktop` are independent. You can run a program and
write an escape sequence in the same poll. When you set `command` and
leave `desktop` unset, `desktop` resolves to `off`, because a user
inside a multiplexer normally wants the program and not the escape.
Set `desktop` yourself to get both.

### Which `desktop` value your terminal understands

The table below records what each terminal documents. Read it as a
starting point, then test with one alert.

| Terminal | `osc777` | `osc9` | Use |
| --- | --- | --- | --- |
| Ghostty | yes | yes | `osc777` |
| kitty | not confirmed | yes | `osc9` |
| WezTerm | yes | yes | `osc777` |
| foot | yes | yes | `osc777` |
| iTerm2 | no | yes | `osc9` |
| Windows Terminal | behind a setting | no | `osc777`, after you set `compatibility.allowOSC777` to `true` |
| tmux | no | no | `off` plus `command` |

Notes on the table:

- kitty documents OSC 9 as the legacy protocol it accepts. Its own
  documentation does not name OSC 777, so this guide does not claim
  it.
- Windows Terminal gained OSC 777 behind the
  `compatibility.allowOSC777` setting, which starts as `false`. Check
  that your build has the setting before you pick `osc777`.
- tmux eats both sequences. It handles OSC 9 itself and understands
  only the progress payload, and it drops OSC 777. Neither one
  reaches the terminal outside. Inside tmux, leave `desktop` at its
  default of `off` and set `command` to a program such as
  `notify-send`, `terminal-notifier`, or `osascript`. Other
  multiplexers are untested here, so treat them the same way until
  you prove otherwise.
- When you do not know what your terminal accepts, set `desktop` to
  `both`. A terminal that does not know a sequence ignores it.

## Themes

Eight skins ship bundled in the `catppuccin` family (`frappe`,
`latte`, `macchiato`, `mocha`), each with a `-transparent`
sibling that leaves the background to the terminal. To add your
own, drop a YAML file under `<config-dir>/skins/` — the basename
without the `.yaml` extension is the name to set on `theme.name`.

The default is `auto`: at startup a10r asks the terminal for its
background colour and picks `catppuccin-latte` on a light
terminal, `catppuccin-mocha` on a dark one. A terminal that does
not answer the question keeps `catppuccin-mocha`. Detection runs
once, at startup, and never overrides a skin you named yourself.
Set `theme.name` (or pass `--theme`) to any skin name to pin the
choice. The name `auto` is reserved, so a skin file called
`auto.yaml` is never loaded.

A user skin with the same basename as a bundled skin shadows the
bundled one; a10r prints a warning so the override isn't a
silent surprise.

To try a skin without restarting, run `:skin` from inside the TUI.
It switches the live skin for the session and never writes
`theme.name`. See
[keybindings.md](keybindings.md#skin-switch).

## Drop-in fragments (`config.d/`)

Anything you can put in `a10r.yaml` you can also stage as a fragment
under `<config-dir>/config.d/`. At startup a10r walks that directory
recursively, loads every `*.yaml` / `*.yml` file in lexical order, and
folds each onto the base config. Symlinks are followed for both files
and directories so an ops team can keep shared snippets under
`/etc/a10r/snippets/` and link them in via configuration management.

```text
~/.config/a10r/
  a10r.yaml             # base, hand-edited
  config.d/
    10-prod.yaml        # tenant snippet, shipped by config-mgmt
    20-staging.yaml
    site/
      30-overrides.yaml # nested directories are walked recursively
```

Merge rules:

- **Backends** are appended. A duplicate `name` across the base file
  and any drop-in (or across two drop-ins) is **fail-closed**: a10r
  refuses to start and the error names both source files so the
  operator can find the conflict in one edit.
- **Scalar fields** (`defaults.*`, `theme.*`, `log.*`,
  `pages.<name>.poll_interval`, `tui.*`) are last-key-wins. A drop-in
  only overrides the fields it sets — unrelated fields from the base
  survive untouched, so you can ship a snippet that only tweaks
  `defaults.poll_interval` without erasing `defaults.log_format`.
  `defaults.read_only`, `tui.tips`, `tui.terminal_title`,
  `tui.poll_delta`, `tui.remember` and `tui.notify.enabled` are
  one-way (any-true wins) so a drop-in can lock them on but not back
  off — edit the layer that set them. `tui.notify.command` is a list,
  so it follows the column rule below: the last layer that declares
  any element owns the whole argv.
- **Column lists** (`pages.alerts.columns`,
  `pages.group_detail.columns`) replace the whole list, they do not
  append. The last layer that declares any column for a page owns
  that page's column set. A drop-in that sets only
  `poll_interval` leaves the base list alone, and so does an explicit
  empty list: to remove a column, edit the layer that declared it.
- **Guardrail rules** (`guardrails`) are concatenated, not replaced.
  A rule only ever tightens what a write may do, so a drop-in can add
  a restriction but can never drop one the base file declared. To
  loosen a rule, edit the layer that declared it.
- **Order** is base file first, then drop-ins in lexical order of
  their absolute path. Use a numeric prefix (`10-`, `20-`, …) to pin
  ordering, the same convention as systemd `*.d/` overrides.
- Each fragment is parsed in **strict mode** with the same discipline
  as the base file — a typo in a snippet surfaces at startup.
- Empty / comment-only fragments are skipped, so a placeholder
  snippet does not crash startup before being filled in.
- A missing `config.d/` is fine: operators who do not curate
  drop-ins pay nothing.

## Aliases

Drop a `<config-dir>/aliases.yaml` next to `a10r.yaml` to register
extra `:` shorthands. The file is a single `{short: expanded}` map;
the expanded value is what the cmdbar would resolve if you typed it
into the prompt — the first token must be a built-in alias, anything
after it is pre-pended to the args you type at runtime.

```yaml
prod: tenant prod                       # `:prod` always selects the prod tenant
stg:  tenant staging                    # `:stg`  selects staging
qq:   q                                 # `:qq`   quits (slightly safer than `:q`)
deploy: alerts --state suppressed       # `:deploy` opens alerts pre-filtered to suppressed
deploy2: alerts list --state suppressed # equivalent — `list` is a no-op positional
```

A user short that collides with a built-in (`:alerts`, `:silences`,
`:sil`, `:info`, `:config`, `:skin`, `:reload`, `:tenant`, `:q`, `:quit`, …) is fail-closed: a10r refuses to start
and lists every offending name so you can fix them in one edit. An
expansion that doesn't resolve to a known built-in fails the same
way.

Recognised flags on the built-in aliases:

- `:alerts` — `--state <active|suppressed|unprocessed>` pre-fills the `Shift+F`
  state cycle; `--filter <value>` pre-fills the `/` filter, in any mode the prompt
  accepts. A value whose regex does not compile is rejected with a flash
  and the page does not open.
  Bare positional tokens (e.g. the CLI-style `list`) are accepted and
  dropped so an alias can mirror the headless `a10r alerts list ...`
  shape without learning a TUI-specific dialect.

A user typo on a flag value (`--state foobar`) surfaces as a Warn
flash on submit; the page is not pushed. Unknown flags
(`--severity` etc.) flash the same way — `:alerts` is the only
built-in that interprets flags today, others ignore them.

A missing `aliases.yaml` is fine — operators who don't curate aliases
pay nothing for the feature. `a10r info` reports the resolved entry
count so you can confirm the file landed where a10r is looking.

## Read-only mode

Three sources, any-true wins (one-way):

1. Per-backend `read_only: true`.
2. Top-level `defaults.read_only: true`.
3. CLI flag `--read-only`.

Sources 2 and 3 cover the whole session: a10r hides every Dangerous
binding (silence create / edit / expire) so you can't accidentally
write while triaging.

Source 1 covers one backend. A list page mixes rows from every tenant
in scope, so the bindings stay up and a10r refuses per row, naming
the backend: `silence.create denied on prod: backend is read_only`.
The hint strip drops the key while the cursor or a mark sits on a
frozen backend, and `?` keeps the row with a `[guarded]` suffix. A run
spanning several tenants is refused whole rather than partly applied
— narrow the marks to the writable set. When every configured backend
is read-only there is nothing writable left, so a10r hides the
bindings exactly as sources 2 and 3 do.

For a narrower restriction — one verb, one set of tenants — see
[Guardrails](#guardrails).

## Guardrails

Read-only freezes every write verb on the backends it covers.
Guardrails are the finer tool: they restrict a single write verb on a
single set of tenants, and leave the rest of your setup alone.

```yaml
guardrails:
  - tenants: ["prod-*"]            # glob list; omit to match every tenant
    actions: ["silence.expire"]    # glob list; omit to match every verb
    deny: true                     # refuse the verb outright
    reason: use the change ticket  # shown to the user on refusal

  - tenants: ["prod-*"]
    confirmation: type-tenant-name # make the user type the backend name

  - max_bulk: 20                   # cap the targets of one bulk run
```

| Field | Type | Meaning |
|---|---|---|
| `tenants` | list of globs | Backend names the rule covers. Omitted or empty matches every backend. |
| `actions` | list of globs | Write verbs the rule covers. Omitted or empty matches every verb. |
| `deny` | bool | Refuse the verb. |
| `confirmation` | `plain` or `type-tenant-name` | The confirmation the user must clear. |
| `max_bulk` | positive int | Largest number of targets one bulk run may touch, per tenant. Omit it (or write `0`) to leave bulk uncapped. To block bulk entirely, use `deny`. |
| `reason` | string | Text shown on a refusal. Ignored by `confirmation` and `max_bulk`. |

The verbs are `silence.create`, `silence.update`, `silence.expire`,
and `silence.recreate`. Bulk is not a separate verb: a bulk run
matches the same name as its single form, and only `max_bulk` reads
the number of targets.

The only wildcard is `*`, which matches any run of characters.
Everything else is literal. a10r rejects `?`, `[`, and `\` at load
so a pattern always means what it looks like.

Every rule that matches the tenant and the verb applies together:

- Any `deny` refuses the write.
- The smallest `max_bulk` wins.
- The strongest `confirmation` wins.

Rule order does not change the outcome. It decides only which
`reason` a refusal quotes when two rules deny.

A rule can only tighten. It can raise a verb's confirmation and it
can never lower one, and a `config.d` fragment adds rules to the base
file rather than replacing them.

Read-only is checked first and wins. On a read-only backend a10r
names read-only, never a guardrail: a per-backend `read_only: true`
is evaluated as a deny no rule can be edited around, and its reason
is the one a refusal quotes.

a10r refuses to start on a rule it cannot understand: an unknown verb
or confirmation level, a negative `max_bulk`, or a rule that sets none
of `deny`, `confirmation`, and `max_bulk`. A `tenants` glob that
matches no configured backend is a warning instead of an error, so
you can share one `config.d` fragment across machines that do not all
have every tenant. Run `a10r info` to see the warnings and the active
rules.

## Reloading

Run `:reload` from inside the TUI to re-read this file, your aliases
file, and your keys file. You keep your page stack, your cursors,
your marks, your filters, and your tenant scope.

These apply without a restart:

| Key | Note |
| --- | --- |
| `theme.name` | Repaints at once. The `auto` value is left as it is, because the terminal answered that question at startup. |
| `tui.tips`, `tui.tips_interval` | Rebuilds the hint bar. |
| `defaults.poll_interval`, per-backend `poll_interval`, `pages.<page>.poll_interval` | Restarts the pollers whose interval moved. |
| `defaults.read_only` | Applies at once, including to the pages already on your stack: their dangerous bindings appear or disappear in place. |
| `defaults.bulk_concurrency` | Applies to pages you open after the reload. |
| `guardrails`, per-backend `read_only` | Applies to pages you open after the reload. A per-backend flag is enforced as a guardrail, so a page already open keeps the policy it was built with. |
| `tui.poll_delta`, `pages.<page>.columns` | Applies to pages you open after the reload. |
| Aliases, keys | Swapped as a whole file, so an entry you deleted stops working. |

These need a restart, and `:reload` refuses the whole file when one
of them changed:

| Key | Why |
| --- | --- |
| `backends`: the list itself, and every field of an entry except `read_only` and `poll_interval` | The session built its HTTP clients from these and keeps them for its lifetime. |
| `log.*`, `defaults.log_format` | The audit trail writes to the file the session opened with the encoder it built. Re-opening it mid-session loses the write order. |

These also need a restart, but `:reload` does not refuse them. It
reports success and leaves them as they are, because the session
wired them into the running program at startup:

| Key | Why |
| --- | --- |
| `tui.notify` | The notifier is built once and handed to the app. |
| `tui.terminal_title` | The title writer is built once and handed to the app. |
| `tui.remember` | The state store is opened once, at startup. |

When a refused key changed, `:reload` applies nothing at all and
flashes `reload: backends or log changed, restart a10r`. A partly
applied config is a session that disagrees with its own
configuration, so a10r does not produce one.

An error anywhere stops the reload before it applies anything. That
covers a config that no longer parses, an aliases file with an entry
a10r cannot resolve, and a keys file that names an unknown action.
The flash carries the error and every live value stays as it was.

`:reload` is refused while a silence form is open, with the flash
`reload: close the form first`, because the reload rebuilds the values
the form was opened against.

## Validating a config

```sh
a10r validate -c ~/.config/a10r/a10r.yaml
```

Exits 0 on success, non-zero with a line:column diagnostic
otherwise.

## Inspecting the resolved config

```sh
a10r info
```

Prints the resolved config dir, state dir, log path, alias count,
active theme, remembered tenant scope, the backend list with
capability flags, and the guardrail rules with any tenant glob that
matches no configured backend. When `tui.notify.enabled` is true it
also prints the resolved notify settings: the desktop transport, the
severity floor, the bell, and the notify program. The report names the
program and counts its arguments, but never prints the arguments,
because they can carry a webhook URL or a token.
