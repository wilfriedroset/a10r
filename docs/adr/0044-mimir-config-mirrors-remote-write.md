# 0044 — The `backends:` block mirrors Prometheus `remote_write`

Each entry under `backends:` in `a10r.yaml` takes its auth, TLS, and
proxy fields directly from Prometheus's
[`remote_write`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write)
schema, wire-key for wire-key: `basic_auth`, `authorization`,
`bearer_token`, `headers`, `tls_config`, `proxy_url`, `no_proxy`,
`proxy_from_environment`, `remote_timeout` are peers of `url:` with the
exact YAML names Prometheus accepts. The a10r-only knobs (`prefix`,
`tenant_header`, `tenant`, `capabilities`, `read_only`, `poll_interval`)
slot alongside them. The goal is paste-compatibility: a user lifts a
`remote_write` block out of `prometheus.yml`, drops it under `backends:`,
fixes the URL path, and is done — no key-by-key translation against the
internal CAs, gateways, and proxies they have already validated.

Alertmanager users are Prometheus users; mirroring the schema reuses
muscle memory instead of inventing a parallel one. The cost is a
deliberate schema break from the v0.1 nested `auth:` envelope, accepted
pre-1.0. Fields that make no sense for a read-only TUI poller
(`write_relabel_configs`, `queue_config`, `oauth2`, `sigv4`, the
`*_file` / `*_ref` secret-manager variants, …) are rejected at parse
time with a pointed error rather than silently ignored, so a paste from
a real Prometheus config surfaces the irrelevant fields immediately. At
most one of `basic_auth` / `authorization` / `bearer_token` may be set,
checked in `Backend.Validate` at config-load time (mirroring
Prometheus's `validateAuthConfigs`) so a misconfigured file fails at
load, not first poll. `tenant_header` + `tenant` remain as sugar that the factory
materialises into one `headers` entry; setting both is a loader error.

Considered and rejected: a bespoke auth envelope (`auth: {type, …}`) —
cleaner in isolation, but every user arrives with a `remote_write` block
in hand and a bespoke shape forces translation for zero capability gain.

See [ADR 0028](0028-backend-client-surface.md) for how the
`prefix` / `tenant_header` pair (not a `type:` discriminator) selects the
backend shape, and [ADR 0029](0029-tls-cert-key-reserved.md) for the
`tls_config` field reservations.
