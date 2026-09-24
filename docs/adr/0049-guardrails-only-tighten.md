# 0049 — Guardrails only tighten

Read-only is all-or-nothing, so an operator who wants to protect one
tenant from one verb has to give up writes everywhere. A `guardrails:`
list adds per-tenant rules that deny a write verb, cap a bulk run, or
demand a stronger confirmation. Every rule that matches the tenant and
the verb applies at once, and a rule can only ever restrict.

## Status

Sits under the read-only precedence of `internal/config/resolve.go`:
read-only is checked first and wins, so a10r never names a user rule
on a backend that was already read-only. A denied verb follows the
Dangerous hiding rule of ADR 0043.

In the TUI that precedence is implemented rather than duplicated: the
boot stage compiles each per-backend `read_only: true` into a deny
rule and prepends it to the configured list, because a list page
unions rows from several tenants and a page-wide switch cannot say
"prod is frozen, staging is not". Prepending is what makes the
refusal quote `backend is read_only` rather than a user rule. The
headless path in `cmd/` keeps its own check.

## Consequences

- **Rule order carries no meaning.** Any `deny` wins, the smallest
  `max_bulk` wins, the strongest `confirmation` wins. Order decides
  only which of several reasons a refusal quotes. A first-match-wins
  fold would make a `config.d` fragment's position load-bearing, and
  fragment order is a filename accident.
- **Fragments append, they never replace.** A drop-in adds rules to
  the base file. Letting a fragment replace the list would let a
  laptop-local file drop a restriction the fleet config declared.
- **The only wildcard is `*`.** a10r rejects `?`, `[`, and `\` at
  load, so a pattern can never fail to compile and a `deny` can never
  fail open because its glob was malformed. The rules the TUI
  compiles from `read_only` are exempt: a backend name is free-form,
  so it is escaped into a literal glob instead of rejected.
- **An unmatched tenant glob is a warning, not an error.** One
  `config.d` fragment is shared across machines that do not all have
  every tenant. `a10r info` and `a10r validate` list the warnings, and
  the TUI logs them at startup so the `:config` page shows them.
- **Every other rule error stops startup.** An unknown verb, an
  unknown confirmation level, a `max_bulk` below 1, or a rule that
  restricts nothing is a typo in a safety feature, and a typo that
  silently permits the write is the failure mode worth avoiding.

## Considered and rejected

- **Rules on alert labels or silence matchers** — rejected. Tenant
  and verb are the axes an operator already reasons about, and a
  matcher-aware rule has to re-derive Alertmanager's own matching
  semantics to say anything true.
- **Time-based rules (business hours)** — rejected. A guardrail that
  changes under the clock is a guardrail nobody can predict at the
  moment they press the key.
- **Reuse `read_only` with a verb list** — rejected. Read-only is a
  per-backend boolean with three any-true sources; growing it into a
  policy language would change the meaning of a field users already
  have in their configs.
