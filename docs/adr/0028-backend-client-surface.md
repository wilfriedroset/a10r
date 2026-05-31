# 0028 — Backend client surface: one constructor, config-driven shape

`internal/backend.Client` is a single interface covering read, write,
and capability-gated extended methods, and `backend/factory.Build` is
the only constructor. There is no `NewVanilla` / `NewMimir` split:
the backend type is determined by YAML config alone. Empty `prefix`
plus empty `tenant_header` yields vanilla Alertmanager; a non-empty
prefix (typically `/alertmanager` to absorb Mimir's mount) plus a
configured `tenant_header` (typically `X-Scope-OrgID`) yields Mimir,
multi-tenant. Setting only the prefix yields Mimir, single-tenant.
Each method has exactly one code path — `vanilla.Client` does the
HTTP, and Mimir composes the same client with transport layers
(prefix already baked into the BaseURL string, tenant header injected
by `transport.WithHostPinnedHeaders`).

The prefix is configurable rather than hard-coded because Mimir
operators can change `-http.alertmanager-http-prefix`, and the tenant
header is configurable because a Mimir run with
`-auth.multitenancy-enabled=false` ships no header at all (and other
multi-tenant fronts might use a different name). The schema mirrors
Prometheus's `remote_write` block (see [ADR 0044](0044-mimir-config-mirrors-remote-write.md)) so the same paste-
and-edit muscle memory applies — `prefix:` and `tenant_header:` slot
alongside the auth blocks rather than living under a
`type: vanilla|mimir` discriminator.

`capabilities` on each `backends:` entry are **explicit opt-in
flags**, not probed at startup. `config_api`, `tenant_admin`, and
`ring` each gate one or two Client methods (the config editor, the
tenant overview, the ring status page). Methods return
`backend.ErrUnsupported` when their flag is off, and the TUI checks
`Client.Capabilities()` before offering the action so the menu entry
never lights up against an endpoint the backend refuses to serve. The
post-v0.1 Mimir config editor will replace today's `vanilla.Client`
capability stubs with a Mimir-specific override; the interface shape
is stable across that change because the capability flags are the
seam.

Considered and rejected: (a) separate `NewVanilla` / `NewMimir`
constructors with their own types — forces every call site
(factory, multi-fan-out, pollers, page wiring) to switch on backend
type, scattering polymorphism across the codebase instead of
absorbing it into a config-driven constructor; (b) probing
capabilities at startup by calling each gated endpoint — adds a
startup-time HTTP round-trip per backend, fails noisily on
locked-down deployments where the probe itself is denied, and
couples backend-up-ness to capability discovery; (c) a discriminated
`type: vanilla|mimir` field — would force a schema break the day
someone runs vanilla with a custom prefix, and duplicates information
already encoded by the prefix/header pair; (d) generating the client
from go-swagger — pulls in a large dependency, freezes the surface
to whatever the upstream OpenAPI spec exposes, and the hand-rolled
client is small enough to maintain directly.

## Consequences

Adding a new capability-gated method follows the seam: declare it on the
`backend.Client` interface; have `vanilla.Client` return
`backend.ErrUnsupported` (the safe default for any backend that does not
serve it); add or flip the gating flag in `config.Capabilities` so the
constructor that should expose it materialises a backend whose
`Capabilities()` reports it on; and gate the TUI entry point on
`Client.Capabilities()` so the action never lights up against a backend
that would refuse it. A Mimir-specific override replaces the vanilla stub
when the real implementation lands — the interface shape does not move.
