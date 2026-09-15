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
keys:                              # optional rebindings (empty = use defaults)
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

One part of this is accepted but not yet built: `wide` renders the
column as if it were `false`. It lands in a later release.

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
  `tui.poll_delta` and `tui.remember` are one-way (any-true wins) so a
  drop-in can lock them on but not back off — edit the layer that set
  them.
- **Column lists** (`pages.alerts.columns`,
  `pages.group_detail.columns`) replace the whole list, they do not
  append. The last layer that declares any column for a page owns
  that page's column set. A drop-in that sets only
  `poll_interval` leaves the base list alone, and so does an explicit
  empty list: to remove a column, edit the layer that declared it.
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
`:sil`, `:tenant`, `:q`, `:quit`, …) is fail-closed: a10r refuses to start
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

Read-only hides every Dangerous binding (silence create / edit /
expire) so you can't accidentally write while triaging.

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
active theme, remembered tenant scope, and the backend list with
capability flags.
