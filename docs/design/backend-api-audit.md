---
title: Backend API audit — vanilla Alertmanager and Grafana Mimir
status: draft
audience: a10r designers and contributors
sources:
  - https://github.com/prometheus/alertmanager (local clone at /home/debian/workspace/github.com/prometheus/alertmanager)
  - https://github.com/grafana/mimir (local clone at /home/debian/workspace/github.com/grafana/mimir)
---

# Backend API audit

This document inventories the HTTP surface of the two backends `a10r` must support — vanilla [Prometheus Alertmanager](https://github.com/prometheus/alertmanager) and [Grafana Mimir](https://github.com/grafana/mimir) — and draws the implications for TUI features and the internal client abstraction. File paths are relative to the respective local clones.

The goal is to answer three questions before we write any view code:

1. Which TUI features map to a single API call, and which require client-side orchestration?
2. What is the smallest surface the `a10r` backend client must expose to cover both implementations?
3. Where do vanilla and Mimir diverge — and which features must be gated behind a backend capability flag?

---

## 1. Vanilla Alertmanager — v2 API

### 1.1 Status of v1 vs v2

v1 is fully deprecated. `api/v1_deprecation_router.go` answers every `/api/v1/...` request with HTTP 410 Gone since 0.27.0. **a10r targets v2 only** and requires Alertmanager **>= v0.28.1**. The floor was bumped from v0.27.0 (set by the v1-API removal) to v0.28.1 so it matches the AM version embedded in our oldest supported Mimir (v2.17). One floor across vanilla and Mimir-embedded deployments collapses the E2E test matrix. Full version matrix and rationale in open-question N1.

### 1.2 Endpoint catalogue

All paths are rooted at `/api/v2`. OpenAPI spec: `api/v2/openapi.yaml`. Handlers: `api/v2/api.go`.

| Method | Path | Purpose | Key params / body | Bulk? |
| --- | --- | --- | --- | --- |
| GET | `/status` | instance + cluster status | — | n/a |
| GET | `/receivers` | list receiver names | — | n/a |
| GET | `/alerts` | list alerts | `active`, `silenced`, `inhibited`, `unprocessed` (bool); `filter[]` (matchers); `receiver` (regex) | read-only, no server-side bulk |
| POST | `/alerts` | ingest alerts | array of `PostableAlert` | **yes** — array in one POST |
| GET | `/alerts/groups` | alerts grouped by route | same as `/alerts` plus `muted` | n/a |
| GET | `/silences` | list silences | `filter[]` (matches *silence matchers*, not alert labels) | no |
| POST | `/silences` | create or update silence (update when body `id` is set) | `PostableSilence` | **no — one silence per call** |
| GET | `/silences/{silenceID}` | single silence | — | n/a |
| DELETE | `/silences/{silenceID}` | expire silence | — | **no — one per call** |

### 1.3 Response highlights (TUI-relevant fields only)

- **Alert** (`GettableAlert`): `labels`, `annotations`, `fingerprint`, `startsAt`, `endsAt`, `generatorURL`, `status.state` (`unprocessed | active | suppressed`), `status.silencedBy[]`, `status.inhibitedBy[]`, `receivers[].name`.
- **Silence** (`GettableSilence`): `id`, `matchers[]`, `startsAt`, `endsAt`, `createdBy`, `comment`, `status.state` (`active | pending | expired`), `updatedAt`.
- **Status**: `cluster.status`, `cluster.peers[]`, `versionInfo`, `config.original` (raw YAML string — the only way to see routes and inhibition rules), `uptime`.

### 1.4 Matcher filter syntax

`filter=` query parameters are repeated for AND semantics, each value being a single Prometheus-style matcher. Parser: `matcher/compat/parse.go`.

- Operators: `=`, `!=`, `=~`, `!~`.
- Values containing spaces, commas, or `:` **must** be double-quoted: `filter=alertname="High CPU"`.
- UTF-8 label names need UTF-8 mode (0.27+ flag) or the classic/UTF8 parser fallback.

TUI implication: the filter prompt must quote values automatically and validate regex before submitting.

### 1.5 Authentication

Vanilla Alertmanager ships **no built-in auth** on `/api/v2`. The generated server stubs declare `BasicAuth` and `BearerToken` security schemes but they are never wired up in `cmd/alertmanager`. All auth is expected to be handled by a reverse proxy (nginx, envoy, oauth2-proxy, …). The full set `a10r` is designed to handle is:

- anonymous (default),
- HTTP basic auth,
- bearer token,
- optional custom headers (for gateway-injected auth),
- client certificate / mTLS,
- AWS SigV4 signing (for backends fronted by AWS Managed Prometheus / ADOT).

The v0.1 cut covers anonymous, basic, bearer, and custom headers; mTLS and SigV4 are deferred (open-questions F2 and F3). The auth layer is shaped as a pluggable `http.RoundTripper` so the deferred mechanisms slot in later without touching call sites.

### 1.6 Generated Go client

Package: `github.com/prometheus/alertmanager/api/v2/client` — go-swagger generated, ~5 kLOC across `alert/`, `alertgroup/`, `general/`, `receiver/`, `silence/`. Constructor: `client.NewHTTPClientWithConfig`.

Recommendation: **do not depend on the generated client**. It pulls in go-openapi runtime and `strfmt`, inflates the binary, and gives us little over hand-rolled `net/http` calls. We will copy the model structs we need (or use `api/v2/models` directly, which has no runtime deps) and write a thin client.

### 1.7 What is missing from the v2 API

These gaps are features we either fake on the client or defer:

- **No pagination.** A tenant with 10k active alerts returns them all in one response. Mitigation: push filter selection into the URL, render progressively.
- **No bulk silence create or expire.** See §4.1 below.
- **No route or inhibition-rule API.** Only readable via `config.original` YAML in `/status`. No way to edit.
- **No streaming / watch.** We must poll, 5–10 s feels right; exponential back-off on errors.
- **No alert resend / escalate** endpoint.

---

## 2. Grafana Mimir Alertmanager

Mimir embeds upstream Alertmanager and adds multi-tenancy + per-tenant config management. Routes are registered in `pkg/api/api.go` around line 233; distributor dispatch in `pkg/alertmanager/distributor.go:84-132`.

**Supported Mimir versions: v2.17 (floor) through v3.0.6 (current ceiling).** v2.17 embeds AM v0.28.1; v3.0.6 embeds AM v0.31.1. The AM API surface a10r consumes is unchanged across this range. See open-question N1 for the full version matrix.

### 2.1 Path layout

Two distinct prefixes.

**Upstream-compatible AM API** — under `{AlertmanagerHTTPPrefix}/api/v2/...`, default prefix `/alertmanager` (flag `-http.alertmanager-http-prefix`). These paths match vanilla v2 exactly once you strip the prefix. Example: `POST /alertmanager/api/v2/silences`.

**Mimir-only administrative endpoints:**

| Path | Method | Tenant-scoped | Purpose | Source |
| --- | --- | --- | --- | --- |
| `/api/v1/alerts` | GET | yes | fetch tenant AM config YAML + templates | `pkg/alertmanager/api.go` `GetUserConfig` |
| `/api/v1/alerts` | POST | yes | upload/replace tenant AM config | `SetUserConfig` |
| `/api/v1/alerts` | DELETE | yes | delete tenant AM config | `DeleteUserConfig` |
| `/multitenant_alertmanager/status` | GET | no | cluster status HTML | `StatusHandler` |
| `/multitenant_alertmanager/configs` | GET | no | stream all tenant configs | `ListAllConfigs` |
| `/multitenant_alertmanager/ring` | GET/POST | no | hash-ring membership + forget | `RingHandler` |
| `/multitenant_alertmanager/delete_tenant_config` | POST | no | admin config delete | `DeleteUserConfig` |

Config API only exists when `-alertmanager.enable-api=true` (default true in recent Mimir).

Note the path collision: `/api/v1/alerts` on Mimir is the **config** endpoint; vanilla `/api/v1/alerts` is the (now-gone) alert list. The `a10r` client must route by backend type, not by overloading v1 paths.

### 2.2 Tenant model

- Tenant is identified by the `X-Scope-OrgID` request header (Cortex/Mimir convention).
- Extracted via `grafana/dskit` middleware into the request context; used e.g. at `pkg/alertmanager/api.go:72`, `pkg/alertmanager/distributor.go:106`.
- When multitenancy is disabled (`-auth.multitenancy-enabled=false`), Mimir ignores the header and treats every caller as the tenant `anonymous`.

**a10r does not assume Mimir multitenancy is enabled.** An operator may run Mimir single-tenant, in which case `a10r` talks to it the same way it talks to vanilla — just with a non-empty URL prefix. The tenant header is therefore a per-endpoint *optional* client setting, not a flag derived from "the backend is Mimir".

**Multi-tenant fan-out in a single request is NOT supported for Alertmanager endpoints.** The querier supports `X-Scope-OrgID: a|b|c` via tenant federation, but `pkg/alertmanager/distributor.go` always calls `tenant.TenantID` (singular) — never `TenantIDs`. When the user *does* run Mimir multi-tenant and selects more than one tenant, `a10r` fans out client-side.

### 2.3 Replication semantics (informational)

`pkg/alertmanager/distributor.go` tags paths with consistency levels:

- Quorum read: `GET /v2/alerts`, `/v2/alerts/groups`, `/v2/silences`, `/v2/silence/{id}`.
- Quorum write: `POST /alerts`, `POST /alerts?`.
- Unary write: `POST /silences`, `DELETE /silence/{id}`.
- Catch-all unary read: everything else (`GET /status`, `GET /receivers`, UI).

Impact on the TUI: silence writes can land on a single replica; after a POST we should re-query (or trust the returned ID) rather than assume instant convergence across the ring.

### 2.4 Authentication

Mimir enforces **no authentication itself**. It trusts `X-Scope-OrgID` as received. Production deployments gate this with a proxy that injects the header from TLS identity, JWT, or API key. `a10r` should therefore let the user configure, per tenant:

- `X-Scope-OrgID` value,
- optional outer auth (basic / bearer / custom headers) that the proxy in front of Mimir requires.

### 2.5 `mimirtool` as design reference

`pkg/mimirtool/commands/alerts.go` has `alertmanager get/delete/verify/migrate-utf8` and `alerts verify`. The client under `pkg/mimirtool/client/alerts.go` provides `GetAlertmanagerConfig`, `CreateAlertmanagerConfig`, `DeleteAlermanagerConfig` — covering the Mimir-only config API. We will **not** import mimirtool (unwanted CLI deps), but it is a useful reference for request shapes. Payload shape for config: `pkg/alertmanager/api.go` `UserConfig` struct — YAML with `alertmanager_config` and `template_files` map.

### 2.6 The rest is upstream

Listing alerts, silencing, receivers, status — all behave the same as vanilla once you add the prefix and the tenant header. A single generic HTTP client serves both.

---

## 3. Capability matrix — vanilla vs Mimir

| Capability | Vanilla AM | Mimir AM | a10r gating |
| --- | --- | --- | --- |
| List / filter alerts | `/api/v2/alerts` | `{prefix}/api/v2/alerts` + optional tenant header | always on |
| Alert groups | `/api/v2/alerts/groups` | same, prefixed | always on |
| Silence CRUD | `/api/v2/silences*` | same, prefixed | always on |
| Receivers | `/api/v2/receivers` | same, prefixed | always on |
| Status / version / uptime | `/api/v2/status` | same, prefixed | always on |
| Config read | via `/status.config.original` (read-only YAML) | **`GET /api/v1/alerts`** + templates | `capabilities.config_api` |
| Config write | — | `POST /api/v1/alerts` | `capabilities.config_api` |
| Config delete | — | `DELETE /api/v1/alerts` | `capabilities.config_api` |
| Tenant list / cluster overview | — | `GET /multitenant_alertmanager/configs` | `capabilities.tenant_admin` |
| Ring state | — | `GET /multitenant_alertmanager/ring` | `capabilities.ring` |
| Multi-tenant in one request | n/a | unsupported — client-side fan-out | — |
| Built-in auth | none | none | proxy concern |

---

## 4. TUI feature mapping

### 4.1 Features that map 1:1 to a single call

- **Alerts view** — table backed by `GET /alerts`. Columns: state, severity, alertname, instance, startsAt, age, receivers. Filters from the `/` prompt translate directly to `filter=` params. Toggle switches (`active`, `silenced`, `inhibited`) map to query params.
- **Alert detail** — drill-in from fingerprint; no extra call needed, we already have the full object in the list response.
- **Groups view** — `GET /alerts/groups`, rendered as a two-level tree (group labels → alerts). Cheaper than grouping client-side at high alert counts.
- **Silences view** — `GET /silences`, sort by state (active → pending → expired).
- **Silence create/edit form** — `POST /silences` (omit `id` to create, set `id` to update).
- **Silence expire** — `DELETE /silences/{id}`.
- **Receivers view** — `GET /receivers`, trivial list.
- **Status pane** — `GET /status`: version, uptime, cluster peers, config excerpt (pretty-printed YAML, read-only).

### 4.2 Features that require client-side orchestration

- **Multi-select bulk silence.** No bulk endpoint on either backend. We will:
  1. Let the user multi-select rows (space bar toggles, `a` selects-all-filtered, same mnemonics as k9s).
  2. Prompt once for silence duration / creator / comment.
  3. Derive one silence per fingerprint (or optionally one silence whose matchers are the intersection of selected alerts' labels — user choice).
  4. Fan out `POST /silences` with a bounded worker pool (e.g. 8 in flight), show a progress bar, report per-item failures.
- **Multi-select bulk expire.** Same pattern, `DELETE /silences/{id}` per item.
- **Group-based silence.** From the groups view, `s` on a group silences by the group's common labels — single `POST /silences` call (cheap, belongs to §4.1 really).
- **Group-by ad-hoc label.** `/alerts/groups` only groups by *route*. User-chosen group-by (e.g. `severity`, `cluster`) is done client-side over the flat `/alerts` response.
- **All-tenants view.** When several backends are configured (each with its own `tenant_header` / `tenant`), fan out `GET /alerts` per backend; merge with a synthetic `tenant` column. Bounded parallelism, per-tenant error surfaced in the flash line. Also used when a single Mimir backend is paired with multiple tenant entries in the config.

### 4.3 Capability-gated features

Hidden from the menu unless the corresponding `capabilities.*` flag is set in the backend config. None of these are "Mimir-only" in principle — any compatible backend that exposes the same paths can opt in — but today only Mimir implements them.

- **Config view / edit** (`capabilities.config_api`). `GET /api/v1/alerts` yields YAML; open in an editor buffer; `POST /api/v1/alerts` saves. `DELETE /api/v1/alerts` with confirmation. Requires Mimir with `-alertmanager.enable-api=true`.
- **Tenant overview** (`capabilities.tenant_admin`). `GET /multitenant_alertmanager/configs` as an admin list of all tenants. Useful as a tenant-switcher source when Mimir multi-tenancy is on.
- **Ring view** (`capabilities.ring`). `GET /multitenant_alertmanager/ring` rendered as a table of instance → state → tokens. `forget` action via POST.

### 4.4 Not provided by either API — potential future work

- Inhibition-rule editor: config-file-only on vanilla, YAML-blob on Mimir. Could be a sub-view of the config editor.
- Alert resend / escalation: neither backend exposes one.
- Watch / push updates: both require polling.

---

## 5. Client abstraction for `a10r`

### 5.1 Smallest viable interface

We will expose a single Go interface in an internal package (exact location tbd when we lay out the tree), roughly:

```go
type Client interface {
    // read
    ListAlerts(ctx, filter AlertFilter) ([]Alert, error)
    ListAlertGroups(ctx, filter AlertFilter) ([]AlertGroup, error)
    ListSilences(ctx, filter SilenceFilter) ([]Silence, error)
    GetSilence(ctx, id string) (Silence, error)
    ListReceivers(ctx) ([]Receiver, error)
    Status(ctx) (Status, error)

    // write
    CreateSilence(ctx, in SilenceSpec) (id string, err error)
    UpdateSilence(ctx, id string, in SilenceSpec) error
    ExpireSilence(ctx, id string) error

    // Extended — return ErrUnsupported when Capabilities() says so
    GetConfig(ctx) (Config, error)
    SetConfig(ctx, Config) error
    DeleteConfig(ctx) error
    ListTenantConfigs(ctx) ([]TenantConfig, error)
    RingStatus(ctx) (Ring, error)

    Capabilities() Caps
}
```

**One constructor, config-driven.** There is no `NewVanilla` / `NewMimir` split — the backend is described in the YAML config:

```yaml
backends:
  - name: prod-am
    url: https://am.internal.example
    # all of these are optional
    prefix: ""                       # e.g. "/alertmanager" for Mimir
    tenant_header: ""                # e.g. "X-Scope-OrgID"; any header name
    tenant: ""                       # value sent in tenant_header
    capabilities:                    # explicit opt-in; nothing auto-enabled
      config_api: false              # /api/v1/alerts GET/POST/DELETE
      tenant_admin: false            # /multitenant_alertmanager/configs
      ring: false                    # /multitenant_alertmanager/ring
    auth:
      type: none | basic | bearer | header | mtls | sigv4
      # …type-specific fields…
```

Rationale for each dial:

- **`prefix`** absorbs Mimir's `/alertmanager` mount without hard-coding it; an operator can also change it via `-http.alertmanager-http-prefix`, so we must not assume `/alertmanager` just because the backend "is Mimir".
- **`tenant_header`** defaults empty (no header sent). The name is configurable not only because Mimir uses `X-Scope-OrgID` while other setups might use something else (`X-Tenant`, gateway-rewritten), but also because a Mimir run with `-auth.multitenancy-enabled=false` needs no header at all.
- **`capabilities`** are explicit flags rather than probed. We do not want to depend on Mimir's multi-tenancy being on, nor on the config API being enabled (`-alertmanager.enable-api=true`). If the operator ticks `config_api: true`, the `Config` / `Tenant overview` views light up; otherwise they stay hidden from the menu. This keeps `a10r` honest against Mimir deployments that have admin APIs disabled, and against future backends we haven't audited.

The resulting client has exactly one code path per method. "Vanilla AM" is just `prefix=""`, `tenant_header=""`, all capability flags off. "Mimir, multi-tenant, admin-enabled" sets the prefix, the header, and the three flags. "Mimir, single-tenant" is identical minus the header and tenant.

### 5.2 Multi-tenant fan-out

A higher-level `MultiClient` wraps `N` per-tenant `Client`s and is used whenever the user selects "all tenants" or a subset. It owns:

- goroutine pool sizing,
- per-tenant timeout + retry,
- merge logic (tagging each result with its tenant),
- error aggregation surfaced to the TUI as a non-fatal flash.

This keeps per-tenant logic out of views.

### 5.3 Transport

Plain `net/http` + `encoding/json`, `context.Context` everywhere, request-scoped timeouts (configurable, default 10 s). Inject auth via a `http.RoundTripper` wrapper so the TUI can show effective headers in a debug pane without touching request-building code.

### 5.4 Models

Copy only what we render from `github.com/prometheus/alertmanager/api/v2/models` (or regenerate a tiny subset) to avoid pulling go-openapi. Times as `time.Time`, label sets as `map[string]string`.

---

## 6. Open questions

These were the questions this audit raised. Each has since been resolved in `docs/design/open-questions.md`; the bullets are kept here as the historical seed and as breadcrumbs to the active decision record.

- Do we support client certificates for mTLS out of the gate, or only on a follow-up? **Resolved in F2** — deferred past v0.1; auth layer keeps the `RoundTripper` shape so it slots in later.
- SigV4 is listed as a planned auth type (§1.5) but deliberately deferred: the maintainer has no immediate need and no AWS account to test against. **Resolved in F3** — deferred; auth layer keeps the `RoundTripper` shape so adding `aws/aws-sdk-go-v2/aws/signer/v4` later is a drop-in.
- How do we display `config.original` on vanilla? Read-only viewer is trivial; do we try to parse routes for a pretty tree view now, or defer? **Resolved in I1** — raw YAML in a read-only viewport for v0.1; structured tree view deferred.
- For Mimir config write, do we validate YAML client-side before POST? **Resolved in I2** — yes, parse-check with `gopkg.in/yaml.v3` (already in the dep graph) before POST.
- Watch-style updates: confirm default poll cadence on `/alerts` is acceptable for the expected alert counts. **Resolved in I3 / C1** — 1 min default, configurable per backend; revisit with real data.

---

## 7. References

- Vanilla AM OpenAPI: `prometheus/alertmanager/api/v2/openapi.yaml`
- Vanilla AM handlers: `prometheus/alertmanager/api/v2/api.go`
- Vanilla AM matcher parser: `prometheus/alertmanager/matcher/compat/parse.go`
- Mimir route registration: `grafana/mimir/pkg/api/api.go:233-259`
- Mimir AM distributor: `grafana/mimir/pkg/alertmanager/distributor.go:84-132`
- Mimir AM config API: `grafana/mimir/pkg/alertmanager/api.go`
- Mimir tenant extraction: `grafana/mimir/pkg/alertmanager/multitenant.go:965`
- Mimirtool reference: `grafana/mimir/pkg/mimirtool/commands/alerts.go`, `pkg/mimirtool/client/alerts.go`
