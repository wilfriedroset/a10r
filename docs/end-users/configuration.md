# Configuration

a10r reads a single YAML file. Every string field is environment-
interpolated via `${VAR}` and `${VAR:-default}` so credentials
need not live in the file.

## File location

Resolution order (first match wins):

1. `--config-dir <path>` (CLI flag).
2. `$A10R_CONFIG_DIR`.
3. `$XDG_CONFIG_HOME/a10r/` (Linux/macOS) or `%APPDATA%\a10r\`
   (Windows). Default `~/.config/a10r/`.

The file inside the resolved directory is `a10r.yaml`.

## Schema

```yaml
backends:
  - name: prod                     # required, identifier
    url: https://alertmanager.example/api/v2   # required, base URL
    prefix: /api/prom              # optional, prepended to every path (Mimir)
    tenant_header: X-Scope-OrgID   # optional, header name (Mimir)
    tenant: tenant-1               # optional, tenant value sent under the header
    capabilities:                  # optional, opt-in beyond AM v2
      config_api: false
      tenant_admin: false
      ring: false
    auth:                          # optional
      type: basic                  # one of: none, basic, bearer, header
      basic:
        username: alice
        password: ${AM_PASSWORD}
    read_only: false               # optional, force read-only for this backend
    poll_interval: 30s             # optional, override defaults.poll_interval
defaults:
  poll_interval: 1m                # default for every backend
  read_only: false                 # default; --read-only flag still wins
  log_format: logfmt               # logfmt or json
theme:
  name: catppuccin-mocha           # bundled or under <config-dir>/skins/
log:
  path: /var/log/a10r.log          # default: $XDG_STATE_HOME/a10r/a10r.log
  level: info                      # debug, info, warn, error
keys:                              # optional rebindings (empty = use defaults)
```

## Authentication

Four modes:

```yaml
# No auth (the default; auth: block omitted entirely).

# HTTP Basic.
auth:
  type: basic
  basic:
    username: alice
    password: ${AM_PASSWORD}

# Bearer token.
auth:
  type: bearer
  bearer:
    token: ${AM_TOKEN}

# Arbitrary header (e.g. for proxy auth).
auth:
  type: header
  header:
    name: X-Auth-Token
    value: ${AM_TOKEN}
```

mTLS and SigV4 are deferred for a future release; the schema
preserves the slot.

## Multi-backend

```yaml
backends:
  - name: prod
    url: https://am-prod.example/api/v2
  - name: staging
    url: https://am-staging.example/api/v2
  - name: dev
    url: https://am-dev.example/api/v2
```

`Ctrl+T` opens the tenant picker; `0` selects every configured
tenant; `1`/`2`/`3` quick-switch to the Nth. When more than one
tenant is selected, the alerts and silences tables surface a
synthetic `tenant` column so you know which backend each row
came from.

## Themes

Three skins ship bundled: `catppuccin-mocha` (default),
`catppuccin-latte`, `gruvbox-dark`. To add your own, drop a YAML
file under `<config-dir>/skins/` — the basename without the
`.yaml` extension is the name to set on `theme.name`.

A user skin with the same basename as a bundled skin shadows the
bundled one; a10r prints a warning so the override isn't a
silent surprise.

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

Prints the resolved config dir, log path, backend list with
capability flags, and the active theme.
