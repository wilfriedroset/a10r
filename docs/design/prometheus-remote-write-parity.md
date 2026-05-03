---
title: Prometheus `remote_write` parity for the `backends:` block
status: accepted
audience: a10r maintainer and contributors
---

# Prometheus `remote_write` parity

## 1. Motivation

Alertmanager users are Prometheus users. Their muscle memory — and the
configuration blocks they have already validated against their internal
CAs, gateways, and proxies — is shaped by Prometheus's
[`remote_write`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write)
schema.

The goal of this design is **paste-compatibility**: a user copies a
`remote_write` block out of their `prometheus.yml`, drops it under
`backends:` in `a10r.yaml`, adjusts the URL path (Alertmanager v2 root
rather than the remote-write receive endpoint), and is done. No
key-by-key translation, no schema impedance.

This forces a schema break — the v0.1 `auth:` envelope is incompatible
with Prometheus's parallel `basic_auth:` / `authorization:` siblings —
which is acceptable pre-1.0 and tracked in open-questions F4.

## 2. Field mapping

### 2.1 In scope

The Prometheus shape we adopt directly. Field names are wire-level:
`a10r.yaml` accepts the exact YAML key Prometheus accepts.

| Prometheus field | a10r v0.1 | a10r after this design | Notes |
|---|---|---|---|
| `url:` | `url:` | `url:` | unchanged |
| `name:` | `name:` | `name:` | unchanged |
| `remote_timeout:` | (hard-coded 30 s in `vanilla.Client`) | `remote_timeout:` | per-backend, with the same 30 s default |
| `headers:` (map) | (only single-value `auth.header`) | `headers:` (map) | reject `Authorization` — auth must use the auth blocks |
| `basic_auth: {username, password}` | nested `auth: {type: basic, basic: {…}}` | `basic_auth:` peer of `url:` | |
| `authorization: {type, credentials}` | (Bearer-only via `auth: {type: bearer, bearer: {token}}`) | `authorization:` peer | generic — any auth scheme; `type:` defaults to `Bearer` |
| `bearer_token:` | (no peer; only via the nested envelope) | `bearer_token:` peer | sugar for `authorization: {type: Bearer, credentials: …}` |
| `tls_config: {ca, cert, key, server_name, insecure_skip_verify, min_version, max_version}` | (none) | `tls_config:` | inline strings only — see §3 |
| `proxy_url:` | (Go default transport reads `HTTP_PROXY` env) | `proxy_url:` | |
| `no_proxy:` | (env-only) | `no_proxy:` | |
| `proxy_from_environment:` | (always-on env honouring) | `proxy_from_environment:` | explicit opt-in matches Prometheus default `false` |

a10r-specific knobs preserved unchanged: `prefix:`, `tenant_header:`,
`tenant:`, `capabilities: {…}`, `read_only:`, `poll_interval:`. None
of these have Prometheus equivalents — `prefix:` is Mimir-shaped,
`capabilities:` and `read_only:` are TUI-shaped, `poll_interval:` is
the polling-loop knob.

`tenant_header:` + `tenant:` remain as YAML sugar for one entry of
`headers:`. A user who copy-pastes a Prometheus block writes
`headers: {X-Scope-OrgID: tenant-1}` directly; a user starting from
`a10r` documentation can use the dedicated keys. The factory layer
materialises the sugar into the same `headers` map at construction
time. Setting both `tenant:` and a colliding `headers:` entry is a
loader error.

### 2.2 Out of scope (drop without aliasing)

These exist in Prometheus but make no sense for a TUI poller. The
loader rejects them with a clear "this field is not used by a10r"
error rather than silently ignoring them, so a paste from a real
Prometheus config surfaces the irrelevant fields immediately:

- **Write-side:** `write_relabel_configs`, `send_exemplars`,
  `send_native_histograms`, `round_robin_dns`, `protobuf_message`,
  `queue_config`, `metadata_config`. a10r reads, never writes time
  series.

### 2.3 Out of scope (deferred, not aliased)

Real `remote_write` features that we explicitly do not implement in
this iteration. Each is a separate decision and lands later via the
same auth and transport-layer extension points:

- `oauth2:` — heavyweight (token URL, scopes, JWT bearer grants);
  defer alongside SigV4. The `Authorization: Bearer …` path covers
  pre-issued tokens for now.
- `sigv4:` — already deferred per F3.
- `azuread:` — same justification as SigV4.
- `google_iam:` — same justification as SigV4.
- `follow_redirects:` — Go's default is to follow; Prometheus's
  default is also `true`. Functionally aligned without configuration.
- `enable_http2:` — Go's default is to enable; Prometheus's default is
  also `true`. Same reasoning.
- `proxy_connect_header:` — niche; revisit if asked.
- `http_headers:` (the structured alternative to `headers:`) — the
  flat map covers the realistic cases.

### 2.4 Out of scope per user instruction (no `*_file` / `*_ref`)

Prometheus's file-path variants exist for k8s-mounted secrets:
`username_file`, `password_file`, `credentials_file`,
`bearer_token_file`, `client_secret_file`, `ca_file`, `cert_file`,
`key_file`, plus the `*_ref` secret-manager variants.

a10r's credential sourcing is fixed by F1: inline values with
`${ENV_VAR}` interpolation. The `*_file` and `*_ref` keys would
duplicate that mechanism without adding capability, so the loader
rejects them with a pointer to F1 in the error message rather than
silently dropping them.

The lone exception, when mTLS lands per F2, will be `tls_config.cert_file`
/ `tls_config.key_file` — those are inherently file-based and decided
in F2's resolution. **Until F2 lands, the loader rejects every
`*_file` and `*_ref` key.**

## 3. Schema decisions

### 3.1 Auth blocks are peers, exactly one is non-nil

Mirroring Prometheus's `validateAuthConfigs`: at most one of
`basic_auth`, `authorization`, `bearer_token` may be set. The loader
runs the check inside `Backend.UnmarshalYAML` so a misconfigured file
fails parse, not first poll.

`bearer_token:` is sugar for `authorization: {type: Bearer,
credentials: <token>}`. After loading, the resolver collapses any
`bearer_token:` into `Authorization` so downstream code (transport,
factory, redaction) has one shape to handle.

### 3.2 `tls_config:` is inline-only in this iteration

Inline-only fields:

- `ca:` — PEM-encoded CA bundle as a single string (use YAML block scalars).
- `server_name:` — SNI override.
- `insecure_skip_verify:` — bool.
- `min_version:` / `max_version:` — `TLS10` … `TLS13` (matches
  Prometheus's `TLSVersion` strings).

`cert:` and `key:` are accepted in the schema but reserved — the
loader rejects them until F2 lands so the YAML key is part of the
contract from day one. (Symmetric with how `Keys{}` was reserved as
an empty struct in v0.1.)

### 3.3 Proxy block flat under `Backend`

Prometheus inlines `ProxyConfig` into `HTTPClientConfig`, so
`proxy_url:`, `no_proxy:`, `proxy_from_environment:` sit at the same
level as `url:`. We follow that exactly. Validation matches
Prometheus's: `no_proxy` requires `proxy_url`;
`proxy_from_environment: true` is exclusive with `proxy_url:` /
`no_proxy:`.

### 3.4 `headers:` is a flat `map[string]string`

Reserved keys rejected at load time:

- `Authorization` (case-insensitive) — must go through `basic_auth:` /
  `authorization:` / `bearer_token:`.
- `Host`, `Content-Type`, `Content-Length`, `Content-Encoding` —
  matches Prometheus's `reservedHeaders` list.

`User-Agent` is NOT in the reserved list — Prometheus doesn't reserve
it either, but a10r's transport layer overrides any caller-supplied
`User-Agent` per `transport.WithUserAgent`'s contract. A user-supplied
`User-Agent` in `headers:` is silently overridden; this matches the
existing v0.1 behaviour.

### 3.5 `remote_timeout:` per backend

Replaces vanilla.Client's hard-coded 30 s. Default stays 30 s when
the field is absent. No global default knob — backends in different
networks legitimately want different timeouts, and there is no useful
fall-through ladder.

## 4. Migration impact

This is a breaking change to `a10r.yaml`. Pre-1.0 users have no
backwards-compat guarantee, so the migration is a one-shot rewrite
rather than a deprecation window:

- `examples/demo.yaml` — no auth, only `url:` / `prefix:` change is
  cosmetic; effectively no change.
- `examples/two-tenants-basic-auth.yaml` — flatten `auth:` into
  `basic_auth:`.
- `examples/local-am.yaml`, `examples/alertmanager.yml` — review and
  flatten as needed.
- The wizard (`internal/tui/wizard/`) writes the new shape from day one.
- `internal/tui/page/tenantconfig/` redacts the new field set.
- `cmd/info` reports the new fields.

CHANGELOG entry under "Breaking changes" anchors the migration with
a before/after diff.

## 5. Future work

- **F2 mTLS resolution** lands `tls_config.cert_file` /
  `tls_config.key_file` and removes the schema-reserved-but-rejected
  status of `cert:` / `key:`.
- **F3 SigV4** lands `sigv4:` peer of `basic_auth:`.
- **OAuth2** ungated when an a10r user actually asks. The schema slot
  is left open via `oauth2:` being rejected with "not yet
  implemented" (rather than "unknown field") so the error message can
  point at this doc.
- **Prometheus secret-manager `*_ref`** is a non-goal — F1's
  resolution rules out external secret backends in favour of `${ENV}`
  interpolation. Revisited only if a real user asks.
