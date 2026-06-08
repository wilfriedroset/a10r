# 0027 — Config resolution precedence: CLI > env > file > default

For every value that has both a CLI form and a config-file field
(`--read-only`, `--log`, `--log-format`, `--poll-interval`, `--theme`,
`--tenant`, `--config-dir`), `internal/config.Resolve` materialises a
single `Effective` by folding sources in the fixed order **CLI flag →
env var (where defined) → config file → built-in default**. The
resolved struct is what every downstream consumer reads — the
logger, the theme loader, the poller, the page ReadOnly gate — so no
subsystem ever inspects raw flags or raw file values, and a missed
fold step manifests at startup rather than as a silent ignore. The
env-slot for `ConfigDir` is `A10R_CONFIG_DIR`; the OS defaults under
it are `$XDG_CONFIG_HOME/a10r` on Unix, `~/Library/Application
Support/a10r` on macOS, and `%LOCALAPPDATA%\a10r` on Windows. The
output format follows the same shape with one extra layer for agents —
`--output` flag → `A10R_OUTPUT` env → agent detection → TTY-derived
default; see ADR 0045.

`--read-only` is "loud over silent": any `true` source forces the
whole session read-only and cannot be reverted by a later source in
the chain. A garbage `A10R_READ_ONLY` value surfaces
`ErrInvalidReadOnlyEnv` at resolve time rather than being parsed as
falsy — typos must not silently weaken the write gate. `--debug`
and `--quiet` are mutually exclusive but reconciled *before* Resolve
runs, in `reconcileLogLevelFlags` (called from cobra's
`persistentPreRun`): if both are set `--debug` wins and a warning is
emitted, so the resolver only ever sees one of them true.
`--poll-interval` overrides only `defaults.poll_interval`;
per-backend `poll_interval` entries still win for their own backend
(the factory layers the mix).

Three flags intentionally bypass the precedence chain: **`--tenant`**
is a session-scope override that should not persist into
`a10r.yaml`, **`--debug-http`** wraps the transport's debug logger at
runtime and has no durable equivalent (and no env-var sibling — debug
transport logging is per-invocation context, not durable config), and
**`--no-pager`** is per-invocation terminal context. The resolver
passes all three through unchanged so callers read what they bound to
cobra. Each exemption is named in `CLIFlags`' field comments so a
contributor adding a new flag knows the default expectation (precedence
applies unless documented otherwise).

Considered and rejected: (a) file > CLI precedence — some daemons
invert to keep deployment configs authoritative, but a10r is an
operator tool where a one-shot override on the command line should
always win over the durable file; (b) merging Tenant / DebugHTTP /
NoPager into the precedence chain to keep the resolver uniform — would
either require a "do not persist" annotation on the field or pollute
`a10r.yaml` with TUI session state when a future `a10r config dump`
serialises Config back to disk; (c) silently treating an invalid
`A10R_READ_ONLY` value as false — the write gate is security-relevant
and a typo dropping read-only mode is exactly the failure mode the
loud-over-silent rule exists to prevent.
