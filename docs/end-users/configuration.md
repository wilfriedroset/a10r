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

The `backends:` block uses the same shape as Prometheus's
[`remote_write`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write) —
copy a `remote_write` entry out of `prometheus.yml`, change the
`url:` to your Alertmanager v2 base, and you are done.

```yaml
backends:
  - name: prod                     # required, identifier
    url: https://alertmanager.example   # required, base URL
    prefix: /api/prom              # optional, prepended to every path (Mimir)
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
  name: catppuccin-mocha           # bundled or under <config-dir>/skins/
log:
  path: /var/log/a10r.log          # default: $XDG_STATE_HOME/a10r/a10r.log
  level: info                      # debug, info, warn, error
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
