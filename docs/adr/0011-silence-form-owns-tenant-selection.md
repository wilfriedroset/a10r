# 0011 — Silence form owns its tenant selection

The silence creation/edit form (`internal/tui/form/silence`) previously
took a single `Options.Client` resolved by the caller via
`pickWriteTarget()` — cursor row's tenant, else first in-scope tenant
alphabetically. That left the user blind to which tenant their submit
would land on, and `docs/design/silence-write-surface.md` §196
explicitly deferred the "tenant prompt on `s` from a multi-tenant
scope" as a future-modal concern. This ADR reverses that deferral:
the form now takes `Options.Clients map[string]Client` plus an
initial `Options.Tenant`, renders a `Tenant:` row as the form's first
field, and on `n` (create) and `Ctrl+N` (recreate-expired) opens the
existing `internal/tui/modal.Picker` via Enter for fuzzy-matched
single-select. The picker lists every writeable tenant regardless of
the current viewing scope — scope is a viewing filter, deliberate
write actions shouldn't be gated by what the operator happens to be
looking at. Single-tenant deployments still render the row but
disabled (greyed), so form layout stays consistent across configs.

The form intentionally **does not** become tenant-aware on `e` (edit)
— a silence cannot move between tenants in the Alertmanager v2 API,
so the row renders read-only. Bulk mode also keeps its existing
`Targets:` banner instead of a Tenant row because the multi-tenant
breakdown is the relevant information there.

Considered and rejected: (a) a resolver callback `func(string) Client`
— harder to assert against in tests; (b) keeping the form ignorant
and emitting `TenantChangedMsg` for the parent page to rewire —
spreads write logic across two files, against the single-source-of-
truth wiring already favoured elsewhere in the TUI. The map-based
shape lives entirely in the form so unit tests can inject a 2-tenant
fake and assert that submit routes to the picker-selected client.
