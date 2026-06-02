# 0039 — `a10r init` drops backend kind; `a10r doctor` verifies the alertmanager mount

The interactive wizard no longer asks `backend kind
(alertmanager/mimir)`. The question was UI-only — the persisted YAML
schema has no `kind:` field — and surveyed users could not tell what
they were being asked for. Worse, picking `mimir` silently injected
`/alertmanager` as the prefix even when the operator had already
encoded the segment in the URL, doubling it. The wizard now asks
only **name, URL, auth, poll, theme**. Operators whose backend is
Mimir put `/alertmanager` in the URL or in the `prefix:` YAML field
directly, the same shape Prometheus's `remote_write` block uses
(ADR 0028).

To keep the bare-Mimir case discoverable, `a10r init` prints a
two-line footer after `wrote …a10r.yaml`: a `prefix:` reminder
(suppressed when the URL path already ends with `/alertmanager`)
and a `tenant_header:`/`tenant:` reminder for multi-tenant setups
(always printed — tenancy cannot be encoded in the URL). The doc
anchor points at `docs/end-users/configuration.md`.

`a10r doctor` carries the verification half. `AuthChecker` already
calls `Status()` and classifies the response; when that call returns
a non-auth 404, the checker now type-asserts `backend.Prober` and
calls `ProbeAlertmanagerMount`, which GETs
`<base>/alertmanager/api/v2/status` with the same configured auth
and headers but ignoring the client's prefix. **Only a 2xx retry**
downgrades the Result to `SeverityWarning` with the verified
message `/api/v2/status returned 404 but /alertmanager/api/v2/status
returned 200 — set prefix: /alertmanager in a10r.yaml`. Every other
outcome (retry 401/403, retry 404, retry transport-error) leaves
the original `SeverityError` and original message untouched —
doctor never speculates about a fix it has not verified
end-to-end. The 401/403 branch additionally appends a tenant hint
when `b.TenantHeader` is empty, which is the only honest signal we
can emit without probing tenant IDs (a probe with a guessed
`X-Scope-OrgID` would be hostile).

ADR 0028 rejected probing at startup for **capability discovery**
(does the ring endpoint exist? does config_api respond?). This
ADR's probe is scoped to **config-correctness verification** at
operator-initiated `doctor` time, behind an already-failed auth
check, with a single extra round-trip. Different problem, same
file. The `backend.Prober` godoc broadens from "liveness probes"
to "doctor-time probes" to reflect the wider scope honestly.

Considered and rejected: (a) **keep `--kv kind=` as a no-op alias**
— a dead branch in the kv parser and a half-finished compat shim
for a pre-1.0 surface that nobody scripts; the loud
`unknown key "kind"` error is the correct migration signal.
(b) **runtime per-poll hint** wrapping 404s in
`vanilla.classifyStatus` with "did you mean prefix:" — repeats on
every poll until fixed and false-positives on legitimate
"no such silence" 404s elsewhere on the surface; the init-time
footer reaches the operator in their setup mental state and the
doctor probe verifies. (c) **new `backend.MountProber` interface**
parallel to `Prober` — same lone implementer, same lone consumer,
no behavioural payoff. (d) **enrich `ReachabilityChecker` instead
of `AuthChecker`** — `ProbeReady` targets `/-/ready` outside the
prefix, which a bare Mimir server happily 200s on; the actual
failure surface for the no-prefix case is `Status()` inside
`AuthChecker`, so that is where the probe earns its place.
(e) **always emit the hint regardless of retry outcome** — doctor
would claim a fix it cannot prove, defeating the entire reason to
probe rather than just suggest.
