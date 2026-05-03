---
title: Open design questions for a10r
status: open
audience: a10r maintainer and contributors
---

# Open design questions

Each item is a decision that has not been made and that downstream work depends on. Items are grouped by category and ordered roughly by what blocks first — resolve top-down where possible. Each item has just enough context to decide, plus a **Lean** when one of the existing design docs already pointed in a direction.

When closing a question, replace its **Status** line with `Resolved YYYY-MM-DD — <one-line outcome>` and move the rationale into the relevant design doc (or a new one) rather than expanding this file.

---

## A. MVP scope

### A1. What ships in v0.1?

**Context.** The k9s audit (§4) lists six view families (table, tree, logs, YAML, pulse, forms). The backend audit (§4.1) lists eight 1:1 features. We have not pinned which are in the first usable release.

**Options.** Minimal — alerts list + alert detail + silences list + silence form + status. Mid — also receivers + groups view. Maximum — also Pulse-style dashboard and Mimir config editor.

**Blocks.** Effort estimate, top-level page list, first commit plan.

**Status.** Resolved 2026-04-25 — mid scope (alerts list, alert detail, silences list, silence form, status, receivers, groups view). Pulse and Mimir config editor deferred.

### A2. Silence: top-level view or drill-down only?

**Context.** k9s makes events-equivalents top-level via `:` aliases. Drill-down only is simpler but discoverability is worse.

**Options.** (a) top-level view (`:sil`) with its own list, (b) drill-down from an alert only, (c) both.

**Blocks.** Page list (A1), command alias registry.

**Status.** Resolved 2026-04-25 — both: `:sil` lists all silences, and from an alert, a key opens the silence(s) currently affecting it.

### A3. Pulse-like dashboard from day one or defer?

**Context.** k9s audit §13. Pulse renders sparklines per receiver/severity over time. Cheap conceptually, lots of pixel work.

**Lean.** Defer past v0.1 — alert-list UX is the load-bearing surface.

**Status.** Resolved 2026-04-25 — deferred past v0.1. Eye candy that does not fit the MVP.

---

## B. Config and defaults

### B1. Config file layout

**Context.** k9s uses 5+ files (`config.yaml`, `aliases.yaml`, `hotkeys.yaml`, `views.yaml`, `plugins.yaml`, plus per-context overrides). The k9s audit recommended "one file with sections" for a10r. The backend audit shows a `backends:` sketch but nothing more.

**Options.** Single `a10r.yaml` with sections (`backends`, `keys`, `theme`, `defaults`, `log`); or split.

**Lean.** Single file at v0.1; split if any section grows past ~50 lines.

**Blocks.** Config loader, schema doc.

**Status.** Resolved 2026-04-25 — single `a10r.yaml` with `backends`, `keys`, `theme`, `defaults`, `log` sections. Split only if a section grows unwieldy.

### B2. Default config search path

**Context.** Unstated anywhere. XDG-conformant: `$XDG_CONFIG_HOME/a10r/a10r.yaml` then `~/.config/a10r/a10r.yaml`.

**Lean.** XDG, with `--config <path>` override and `A10R_CONFIG` env var.

**Blocks.** First-boot UX, install docs.

**Status.** Resolved 2026-04-25 — k9s-style XDG resolution. Config *directory* per OS:

- Unix: `$XDG_CONFIG_HOME/a10r` (default `~/.config/a10r`)
- macOS: `~/Library/Application Support/a10r`
- Windows: `%LOCALAPPDATA%\a10r`

Override with `--config-dir <path>` flag or `A10R_CONFIG_DIR` env var. Main file inside the directory is `a10r.yaml`. Provide an `a10r info` subcommand that prints the resolved directory (matches `k9s info`).

When no config exists on first run, launch an in-TUI wizard to capture the first backend (URL, optional prefix, optional tenant header + value, auth) and write `a10r.yaml`.

### B3. Per-context config files (k9s-style) or single connection list?

**Context.** k9s writes per-cluster overrides to disk. a10r has fewer dimensions to vary per backend.

**Lean.** Single file with a `backends:` array; per-backend overrides nested under each entry.

**Status.** Resolved 2026-04-25 — single `backends:` array in `a10r.yaml`, all per-backend settings inline. No separate per-backend override files.

---

## C. Runtime behaviour

### C1. Poll lifecycle under bubbletea

**Context.** Comparison doc names `Program.Send(alertsRefreshedMsg{...})` from a poll goroutine but does not spec: tick interval source (config? per-view?), error backoff (linear / exponential / cap), jitter to avoid thundering herd against shared backends, cancellation on view switch / quit, reconnection signalling to the UI.

**Blocks.** First page implementation.

**Status.** Resolved 2026-04-25:

- **Tick interval** — `defaults.poll_interval` global, per-backend override on each `backends:` entry. Default 1 min (safer for weak backends; tune down per-backend when justified).
- **Error backoff** — exponential, base 1 s, cap at `poll_interval × 6`. Reset on success.
- **Jitter** — ±10 % on every tick to avoid synchronized hammering across instances.
- **Cancellation** — each backend poller owns a `context.Context` derived from the program root. Program quit cancels root → tears down all pollers. View switches do **not** cancel pollers; views subscribe to messages instead.
- **Reconnection signalling** — pollers emit `backendStatusMsg{name, state, since}` with state ∈ `{connected, degraded, unreachable}` on every transition. Header component subscribes and renders the indicator. Flash fires only on transitions, not per failed retry.

### C2. Disconnection UX

**Context.** When a backend is down, what does the user see? Greyed table? Last-known data with a "stale since X" badge? Flash on each retry?

**Lean.** Last-known data + persistent header indicator + flash on state transitions only (don't spam retries).

**Blocks.** Header/status component.

**Status.** Resolved 2026-04-25:

- Always render last-known data; never blank the table on a failed poll.
- Header indicator per backend: `● connected` (green) / `◐ degraded` (yellow) / `○ unreachable` (red), with `<count> · <age>` next to it.
- **Degraded** = 2 consecutive failed polls.
- **Unreachable** = backoff cap reached (per C1).
- On unreachable, dim table text by ~40 % so it reads as stale.
- Flash fires once on each transition (`lost connection to <backend>`, `<backend> back`); never per retry.

### C3. Multi-tenant TUI affordance

**Context.** Backend audit §4.2 commits to client-side fan-out. The TUI side is unspecified: how does the user select "all", a subset, or one tenant? What appears in the header? Does the table grow a synthetic `tenant` column always, or only when more than one is selected? In a10r terms, each `backends:` entry is one (URL + optional tenant header + tenant value) tuple — "tenant" in the UI maps to one such entry.

**Options.** (a) `:tenant <name|all>` command, (b) tab strip across the top, (c) numeric quick-switch (`1`..`9`), (d) combination.

**Blocks.** Header design, command registry.

**Status.** Resolved 2026-04-25 — combination:

- `:tenant <name>` selects one; `:tenant all` selects all; `:tenant a,b,c` selects a subset.
- `:tenant` (no args) opens a tenant table view listing every configured tenant with state and counts (mirrors k9s `:ns`). Selection from the table updates the active set.
- Numeric quick-switch: `0` = all, `1`..`9` = first nine configured tenants in declaration order. Mirrors k9s namespace switching.
- `Ctrl-T` opens a modal picker with fuzzy search — Enter single-selects, Space toggles for multi-select, `a` selects-all, Esc cancels. Useful past the 9-tenant limit.
- Header: `tenants: prod` / `tenants: prod, staging` / `tenants: all (3)`, truncated with `…+K more` on overflow.
- Synthetic `tenant` column added to the table only when more than one tenant is selected; hidden in single-tenant view.

### C4. Read-only mode location

**Context.** k9s audit recommended a `readOnly: true` flag with dangerous actions filtered. Backend audit's config sketch omits it. Need to commit (a) where it lives in the config (top-level? per-backend?) and (b) what counts as dangerous (silence create/expire, Mimir config write).

**Lean.** Per-backend (so a single a10r instance can be read-only against prod and read-write against staging) plus a global override.

**Blocks.** Action registry, B1 schema.

**Status.** Resolved 2026-04-25:

- Per-backend `read_only: true` on each `backends:` entry.
- Global override via `defaults.read_only`, `--read-only` CLI flag, and `A10R_READ_ONLY` env var. Any `true` source forces read-only across the whole session.
- Dangerous actions (filtered from the menu under read-only): silence create / update / expire, plus Mimir config write / delete (deferred past v0.1 but the flag must still gate them).
- Everything else (list, filter, drill, view config, view ring) stays available.

### C5. Caching aggressiveness

**Context.** k9s audit §13. Alertmanager state is small enough that polling may be sufficient. But repeated `GET /silences` to render details on every keystroke is wasteful.

**Lean.** Poll-only, with per-view in-memory cache invalidated on each poll tick. No persistent cache.

**Blocks.** Client design.

**Status.** Resolved 2026-04-25:

- Poll-only. Each tick replaces the in-memory snapshot for that (backend, resource).
- Pages render from the snapshot; drill-down re-uses the same object — no extra GET.
- One in-flight poll per (backend, resource); no request deduplication beyond that.
- Nothing on disk.
- Manual refresh: `r` triggers an immediate poll for the current view's resource, bypassing the tick (logged in J2).

---

## D. Diagnostics

### D1. Log destination

**Context.** Bubbletea owns stdout, so logs cannot go there while the TUI runs. No doc states a default. k9s uses `$XDG_STATE_HOME/k9s/k9s.log`.

**Lean.** `$XDG_STATE_HOME/a10r/a10r.log` with rotation; `--log <path>` to override; `--debug` raises level.

**Blocks.** First-boot UX, troubleshooting doc.

**Status.** Resolved 2026-04-25:

- Path resolution (k9s-style, mirrors B2):
  - Unix: `$XDG_STATE_HOME/a10r/a10r.log` (default `~/.local/state/a10r/a10r.log`)
  - macOS: `~/Library/Logs/a10r/a10r.log`
  - Windows: `%LOCALAPPDATA%\a10r\Logs\a10r.log`
- Override: `--log <path>` flag and `A10R_LOG` env var.
- Rotation: size-based, 10 MB max with 3 keepers.
- Levels: default `info`; `--debug` → `debug`; `--quiet` → `warn`.
- Format: structured. Must support JSON and logfmt output (selectable via `defaults.log_format` or `--log-format`). Library choice (slog vs charmbracelet/log) tracked in D3.
- `a10r info` prints both the config dir (B2) and the log path.

### D2. In-app log viewer

**Context.** Useful when the user can't easily tail the file (ssh, container). k9s does not have one; they rely on the file.

**Lean.** Defer past v0.1; document the file path instead.

**Status.** Resolved 2026-04-25 — deferred past v0.1. Users tail the log file; path discoverable via `a10r info`.

### D3. Logging library: stdlib `slog` vs `charmbracelet/log`

**Context.** Comparison doc originally listed `charmbracelet/log` as "almost certain to use" because it ships pretty TTY output and matches the Charm ecosystem. D1 added the requirement that the log format be selectable between JSON and logfmt. A research pass corrected an earlier assumption: `slog.TextHandler` already emits logfmt-shaped `key=value` output, so no third-party handler is needed for either of the two configurable formats.

**Options.** (a) `log/slog` directly, no extra deps; (b) `charmbracelet/log` as the handler behind a `slog.Logger`; (c) `charmbracelet/log` everywhere, no `slog` adapter.

**Lean.** (b) — pretty TTY for free, near-zero marginal dep cost in our graph.

**Status.** Resolved 2026-04-25 — (a). Configurable formats are JSON or logfmt only; pretty TTY is explicitly **not** wanted as a file-log format because ANSI codes break `grep`/`jq` workflows. Both required formats are stdlib-native (`slog.JSONHandler`, `slog.TextHandler`), so charm/log adds no value and would cost a dep + a Go 1.25 floor we'd otherwise inherit only from bubbletea v2. Comparison doc updated to move `charmbracelet/log` out of the "almost certain to use" tier.

---

## E. Filter and sort

### E1. Filter syntax

**Context.** Backend audit §1.4 documents Alertmanager's `filter=` syntax (Prometheus matchers, quoting rules, regex). The user-facing `/` prompt could mirror it 1:1, or offer a friendlier substring-by-default mode that translates to matchers under the hood.

**Options.** (a) pass-through Alertmanager matchers; (b) substring-default with `=~` opt-in; (c) two modes (`/` substring, `f` matcher).

**Lean.** (b) — substring default, with `=` / `!=` / `=~` / `!~` recognised when present.

**Blocks.** Prompt component.

**Status.** Resolved 2026-04-25 — phased:

- **v0.1**: (b) everywhere. `/` opens a substring filter; bare tokens match any label/value substring; tokens of the form `label=value` / `label!=value` / `label=~regex` / `label!~regex` are recognised as Prometheus matchers. Substring search runs client-side over the cached snapshot; matcher tokens on the alerts list are pushed to `filter=` in the API call to keep payloads small.
- **Post-v0.1**: add mode (c) on the alerts list only — `f` opens a matcher-only prompt for power users / large Alertmanagers where matcher syntax pays off. `/` keeps the (b) behaviour. Other views (silences, receivers, status, groups) stay on (b) — matchers don't apply there.

### E2. Sort persistence and default

**Context.** Per-view sort? Global sort? Persisted across runs?

**Lean.** Per-view in-memory; defaults `startsAt desc` for alerts, `endsAt asc` for silences.

**Blocks.** Table component.

**Status.** Resolved 2026-04-25:

- Per-view, in-memory only. Sort does not survive program restart.
- Defaults: alerts `startsAt desc`, silences `endsAt asc`, receivers `name asc`, groups by group key, tenant table by declaration order.
- Left/Right arrows walk through sortable columns on the active row (k9s convention).
- Per-view shorthand keys to jump-sort by common columns (k9s-style — e.g. `Shift+S` for severity on alerts, `Shift+E` for ends on silences). Concrete bindings enumerated in J2.

---

## F. Auth ergonomics

### F1. Credential sourcing

**Context.** Backend audit §1.5 lists basic, bearer, header, mTLS, sigv4. It does not say where the values come from. Putting tokens in `a10r.yaml` is convenient but ages poorly.

**Options.** (a) inline in YAML with `${ENV_VAR}` interpolation, (b) external file reference, (c) OS keychain integration, (d) interactive prompt on first connect.

**Lean.** (a) for v0.1; (c) as follow-up.

**Status.** Resolved 2026-04-25 — (a). Inline in `a10r.yaml` with `${ENV_VAR}` interpolation on every string value. Matches the kubeconfig pattern k9s relies on. (b)/(c)/(d) deferred. mTLS cert and key paths are an exception decided alongside F2 since those fields are inherently file-based.

### F2. mTLS at v0.1?

**Context.** Backend audit §6 — leaning yes, cheap to plumb via `http.Transport`.

**Lean.** Yes, ship in v0.1.

**Blocks.** Auth layer scope.

**Status.** Resolved 2026-04-25:

- mTLS deferred past v0.1. Auth layer in v0.1 covers `none`, `basic`, `bearer`, `header`. mTLS slots in later via the same `RoundTripper` shape (per backend audit §1.5).
- `auth:` is a single object on each `backends:` entry — not a list. Lists are order-sensitive and ordering would mislead users about precedence.
- When stacking is needed (e.g. mTLS at the transport layer + bearer at the HTTP layer), it nests inside the same `auth:` object via dedicated keys per layer rather than peers in a list. Concrete shape pinned when mTLS is added.

### F3. SigV4

**Context.** Backend audit §1.5 and §6 — deferred. Auth layer is already a `RoundTripper` so SigV4 can slot in later.

**Lean.** Defer; no work in v0.1 beyond keeping the `RoundTripper` shape.

**Status.** Resolved 2026-04-25 — deferred past v0.1. Auth layer keeps the `RoundTripper` shape so `aws/aws-sdk-go-v2/aws/signer/v4` slots in later without touching call sites.

### F4. Prometheus `remote_write` shape parity

**Context.** Alertmanager users are Prometheus users. Their existing `prometheus.yml` already encodes the auth, TLS, proxy, and header conventions of their environment in the `remote_write` block. The v0.1 `auth:` envelope in `a10r.yaml` does not match that shape — `basic_auth:` is nested under `auth.basic`, there is no TLS or proxy block, and `remote_timeout` is hard-coded.

**Options.** (a) adopt Prometheus's shape verbatim — flat `basic_auth:` / `authorization:` / `bearer_token:` siblings, `tls_config:`, `proxy_url:`, etc. — accepting a pre-1.0 schema break; (b) accept both shapes, with the existing `auth:` envelope as a deprecated alias; (c) keep the v0.1 envelope and require users to translate.

**Lean.** (a). Pre-1.0 break is cheap; "two ways to express the same thing" debt aged poorly in every config schema we have watched.

**Blocks.** Schema migration of `internal/config`, `internal/backend/transport`, examples, the first-run wizard, and the tenant-config redactor.

**Status.** Resolved 2026-05-03 — (a). Detailed schema, in-scope / out-of-scope field list, and migration plan in [`prometheus-remote-write-parity.md`](prometheus-remote-write-parity.md). `*_file` and `*_ref` keys remain rejected per F1 (env-var interpolation is the credential-sourcing answer); `oauth2:`, `sigv4:`, `azuread:`, `google_iam:` deferred.

---

## G. Client error model

### G1. Error taxonomy

**Context.** Backend audit §5.1 names the `Client` interface and `ErrUnsupported`. It does not classify retryable vs fatal, transport vs application errors, or how `MultiClient` aggregates failures across tenants.

**Lean.** Three sentinel errors (`ErrUnsupported`, `ErrUnauthorized`, `ErrUnreachable`) plus a `RetryableError` interface. `MultiClient` returns a slice of `(tenant, error)` and lets callers decide whether to surface or proceed.

**Blocks.** Client implementation.

**Status.** Resolved 2026-04-25:

```go
var (
    ErrUnsupported  = errors.New("operation not supported by backend capabilities")
    ErrUnauthorized = errors.New("authentication failed")
    ErrUnreachable  = errors.New("backend unreachable")
)

type Retryable interface { Retryable() bool }
```

- `errors.Is(err, ErrUnsupported)` — menu entry hidden; never retried.
- `errors.Is(err, ErrUnauthorized)` — header indicator red; flash fires; no auto retry until config reload.
- `errors.Is(err, ErrUnreachable)` — drives the C1 backoff loop.
- Anything satisfying `Retryable() bool == true` — also retried per C1 backoff.
- Everything else — one-shot flash with the raw error; logged at warn.

`MultiClient` returns `(per-tenant value, per-tenant err)` slices. Callers decide partial vs bail. Failed tenants never silently swallowed — surfaced in the flash line as `prod-am: unreachable; staging-am: unauthorized`.

---

## H. Test fixtures

### H1. Where do backend fixtures come from?

**Context.** Comparison doc covers TUI tests via `teatest`. Backend audit is silent on how we obtain JSON for the client unit tests.

**Options.** (a) hand-crafted JSON checked in, (b) recorded once from a live Alertmanager via `httptest`-style capture, (c) generated from the OpenAPI schema.

**Lean.** (a) for v0.1 — small fixtures, easy to keep readable. Add (b) later for regression coverage on real-shape responses.

**Blocks.** First client test.

**Status.** Resolved 2026-04-25 — phased:

- **v0.1 (unit tests)**: (a) hand-crafted JSON in `testdata/`. Small, readable fixtures covering active/silenced/inhibited alerts, expired and active silences, and the few known edge cases (UTF-8 labels, missing `endsAt`, large label sets).
- **CI (e2e / integration)**: (b) recorded fixtures from a live Alertmanager (and Mimir for capability-gated paths), replayed via `httptest`. Pulled in once we are ready to wire e2e/integration tests in the CI pipeline.

---

## I. Backend audit leftovers

### I1. Vanilla `config.original` viewer depth

**Context.** Backend audit §6. Display the raw YAML or parse routes for a tree view?

**Lean.** Raw YAML at v0.1 (read-only viewer); structured view as follow-up.

**Status.** Resolved 2026-04-25 — (a) raw YAML in a read-only viewport for v0.1. Structured tree view deferred.

### I2. Mimir config client-side YAML validation

**Context.** Backend audit §6. `gopkg.in/yaml.v3` adds a small dependency.

**Lean.** Yes — parse-check before POST so we surface obviously broken files locally.

**Status.** Resolved 2026-04-25 — yes. Parse-check the YAML locally with `gopkg.in/yaml.v3` (already in the dep graph for `a10r.yaml`) before POSTing to Mimir. Surface parse errors in the editor with line/column. Decision locked now even though the editor itself ships post-v0.1.

### I3. Default poll interval

**Context.** Backend audit §6 — 5 s default to confirm against real alert volumes.

**Lean.** 10 s default, configurable per backend; revisit after first real-data run.

**Status.** Resolved 2026-04-25 — **1 min** default, configurable per backend (and globally via `defaults.poll_interval`, per C1). Bumped from the original 10 s lean to be safe against weak/slow backends; operators tune down where the backend can take it.

### I4. Sort indicators on `bubbles/table`

**Context.** Was "fork tview" in the original audit. Under bubbles, the question is: extend `bubbles/table` to render arrow markers in the header row, or compose a header line above it via lipgloss?

**Lean.** Lipgloss-rendered header line above the table — keeps `bubbles/table` unforked and gives full control over sort/active-column styling.

**Status.** Resolved 2026-04-25 — lipgloss-rendered header line above an unforked `bubbles/table`. Carries column names with sort arrows (`▲ ▼`) on the active column and distinct styling for sorted vs unsorted columns. We own the column-width math; the table follows. Aligns with the project-wide no-fork principle.

---

## J. UX details

### J1. Per-view header content

**Context.** The header shape was settled in C2 (status indicator) and C3 (tenant selector), but each view (alerts, alert detail, silences, silence form, status, receivers, groups, tenant table) needs its own header content — counts, filters in effect, sort column, breadcrumbs, anything else useful at a glance.

**Blocks.** First implementation of each view.

**Status.** Resolved 2026-04-25 — shared skeleton now, per-view content pinned per implementation PR.

Three-zone header skeleton (mirrors k9s's logo + menu-hints split):

- **Left zone**: tenant indicator (from C3) + per-tenant connection state (from C2). Compact, e.g. `tenants: prod ●` or `tenants: all (3)`.
- **Middle zone**: per-view content slot — counts, active filter, sort column, anything else useful at a glance. Specified per view at implementation time, not now.
- **Right zone**: contextual keybinding hint strip auto-built from the action registry (J2). Always includes `[?] help` as the last entry. Hides actions filtered by read-only mode (C4).

Crumbs, prompt, and flash live in the bottom strip (per the k9s audit §2 layout), unchanged by this decision.

### J2. Keybindings catalog

**Context.** k9s audit §3 lists the patterns (arrows, PgUp/PgDn, Enter to drill, Esc to pop, Space to mark, Ctrl-A to mark all, `:` for command, `/` for filter, numeric for quick-switch). a10r additions and overrides need pinning: vim motions (`j`/`k`/`gg`/`G`/`Ctrl-D`/`Ctrl-U` per the k9s audit "Things to reconsider"), `Ctrl-T` for tenant picker (per C3), `0`..`9` for tenant quick-switch (per C3), `s` for silence-from-alert (per A2), `r` for manual refresh (per C5), `f` for matcher-only filter on the alerts list post-v0.1 (per E1), per-view `Shift+<letter>` jump-sort shortcuts (per E2), bulk-select mnemonics, the help overlay key.

**Blocks.** Action registry, help overlay, J1.

**Status.** Resolved 2026-04-25 — full catalog (global + table-context + per-view for all eight v0.1 views) lives in `docs/design/keybindings.md`. Includes precedence, dangerous-action tagging for read-only mode (C4), and reserved-key list for future plugins.

---

## K. CLI surface

### K1. CLI flags and subcommands

**Context.** Several flags and subcommands were locked piecemeal across earlier questions (`--config-dir` in B2, `--log` / `--log-format` / `--debug` / `--quiet` in D1, `--read-only` in C4, `info` subcommand in B2). This question consolidates the full surface and adds the missing pieces: tenant pre-selection, validate, version, completion, and the CLI library choice.

**Blocks.** Scaffold PR (`cmd/a10r/main.go`), shell completion install docs.

**Status.** Resolved 2026-04-25 — built on `spf13/cobra` (matches k9s; gives subcommands, completion, and help for free).

**Subcommands.**

| Command | Purpose |
| --- | --- |
| *(no args)* | Launch the TUI |
| `info` | Print resolved config dir, log path, version, build info (per B2 / D1) |
| `version` | Print version only (machine-readable) |
| `validate` | Parse config; exit 0 on success, non-zero with errors. Intended for CI/CD pipelines that template `a10r.yaml` |
| `completion bash\|zsh\|fish\|powershell` | Emit shell completion script (cobra-generated) |
| `help [command]` | Cobra default |

`init` (write a stub config non-interactively) explicitly **skipped** — the first-run wizard from B2 covers the same need interactively, which is the right shape for the target user.

**Global flags.**

| Flag | Env | Meaning | Source |
| --- | --- | --- | --- |
| `--config-dir <path>` | `A10R_CONFIG_DIR` | Override config directory | B2 |
| `--log <path>` | `A10R_LOG` | Override log file path | D1 |
| `--log-format json\|logfmt` | `A10R_LOG_FORMAT` | Override log format | D1 / D3 |
| `--debug` | — | Set log level to `debug` | D1 |
| `--quiet` | — | Set log level to `warn` | D1 |
| `--read-only` | `A10R_READ_ONLY` | Force read-only across the session | C4 |
| `--tenant <name\|all\|a,b>` | — | Pre-select tenant(s) at startup; mirrors `:tenant` syntax from C3 | K1 |
| `--poll-interval <duration>` | — | Override `defaults.poll_interval` for this run (per C1 / I3) | K1 |
| `-h` / `--help` | — | Cobra default | — |
| `--version` | — | Cobra default | — |

**Precedence rules.**

- `--debug` and `--quiet` are mutually exclusive in intent. If both are set, `--debug` wins and a warning is logged.
- For any flag that has both a CLI form and a config field (`--read-only`, `--log`, `--log-format`, `--poll-interval`), the resolution order is: CLI flag → env var (where defined) → config file → built-in default. Any source resolving to a `true`/non-empty value forces the override; `--read-only` in particular cannot be turned off by config once set on the CLI (matches C4 — any `true` source forces read-only).
- `--poll-interval` overrides only the global `defaults.poll_interval`. Per-backend `poll_interval` overrides on individual `backends:` entries (per C1) still win for that backend — the CLI flag is a session-wide replacement for the global default, not a hammer that flattens per-backend tuning.

---

## L. External editor

### L1. Editor invocation for resource edits

**Context.** Editing resources as YAML in the user's preferred editor is a familiar pattern (k9s, kubectl). v0.1 has two natural surfaces: silence editing (currently a `huh` form per J2) and the Mimir config editor (deferred past v0.1, inherently YAML).

**Blocks.** Silence list `Ctrl+E` binding, Mimir config editor when it lands.

**Status.** Resolved 2026-04-25.

**Resolution order** (first non-empty wins):

1. `$A10R_EDITOR` — project-specific override.
2. `$EDITOR` — Unix convention.
3. Platform fallback: `vi` on Unix, `notepad` on Windows. If neither resolves on `$PATH`, surface a flash error and abort the edit.

**Mechanism.** `tea.ExecProcess` from bubbletea v2 suspends the program, runs the editor synchronously against a tempfile, then resumes. The tempfile lives at `$XDG_CACHE_HOME/a10r/edit-<resource>-<id>.yaml` (default `~/.cache/a10r/...`) and is cleaned on successful submit.

**Cancel and error paths.**

- Editor exits with empty file or no changes vs. the loaded buffer → cancel silently.
- Saved file fails YAML parse → flash with line/column (matches I2 policy for Mimir), offer to re-open the editor.
- Server-side validation fails on POST → flash with the server message, offer to re-open the editor with the user's last buffer.

**v0.1 wiring.**

- **Silences list**: `Ctrl+E` opens the current silence as YAML in `$EDITOR`, alongside `e` (huh form) which stays the friendly default. `Ctrl+E` is **Dangerous** (gated by read-only per C4).
- **Silence form (huh)**: unchanged, still the default for `n` (new) and `e` (edit existing).
- **Mimir config editor**: deferred past v0.1; when it lands it uses this same plumbing.

`Ctrl+N` is reserved in the keybindings catalog for a future "compose new silence as YAML" companion to `Ctrl+E`. Not wired in v0.1.

---

## M. Theming

### M1. Skin system

**Context.** §11 of the k9s audit committed a10r to a "skin system with live reload, 5-6 bundled themes" but never pinned the schema, the bundled set, or the v0.1 scope. k9s itself uses a single-layer schema (every role takes a hex string) and ships 36 themes.

**Blocks.** Renderer plumbing for every component (header, table, prompt, flash, modal, status pane).

**Status.** Resolved 2026-04-25.

- **Schema**: two-layer YAML — `palette` (named colors, theme-private) plus `roles` (a fixed public contract that maps semantic slots to palette names). Lets palette variants (mocha/latte/frappe/macchiato) reuse the role map without copy-pasting.
- **Bundled themes for v0.1** (embedded via `embed.FS`, no file needed): `catppuccin-mocha` (default), `catppuccin-latte`, `gruvbox-dark`. The non-catppuccin entry is there to prove the role map works across palette philosophies.
- **User-supplied skins**: `<config-dir>/skins/<name>.yaml`. User entries with the same name as a bundled theme override the bundled version (a startup warning is logged). Standard "user wins" pattern.
- **Selection**: `theme.name: <string>` field in `a10r.yaml`; `--theme <name>` CLI override (per K1 precedence rules).
- **Live reload**: deferred past v0.1.
- **Adaptive light/dark**: skipped. Explicit theme picks always win; an `auto` keyword can land post-v0.1 alongside live reload.
- **Renderer**: lipgloss (already chosen). Roles compile to `lipgloss.Style` instances at theme-load time; views consume those styles by role name.
- **Schema reference**: full role list, palette conventions, file resolution, and a worked catppuccin-mocha example live in `docs/design/theming.md`.

---

## N. Supported backend versions

### N1. Vanilla Alertmanager and Mimir version matrix

**Context.** Backend audit §1.1 originally pinned the vanilla AM floor at v0.27.0 because that was the release where the v1 API was returned as HTTP 410. With Mimir support in scope, the practical floor is whatever AM ships embedded in the oldest Mimir we promise to work against — anything older buys us nothing because it would never be reachable through Mimir.

**Blocks.** E2E test matrix (per H1 part b), CI image pulls, support statements in the README.

**Status.** Resolved 2026-04-25.

| Backend | Floor | Ceiling | Notes |
| --- | --- | --- | --- |
| Vanilla Alertmanager | **v0.28.1** | v0.32 (current) | Bumped from the original v0.27.0 floor so we have a single AM minimum across vanilla and Mimir-embedded deployments |
| Grafana Mimir | **v2.17** | v3.0.6 (current) | v2.17 embeds AM v0.28.1; v3.0.6 embeds AM v0.31.1 |

**E2E matrix** (gated by the H1 phase-(b) recorded fixtures; live spin-up in CI):

- Vanilla AM v0.28.1 (floor)
- Vanilla AM v0.32 (ceiling)
- Mimir v2.17 (floor; covers AM v0.28.1 path with multi-tenancy + config API)
- Mimir v3.0.6 (ceiling; covers AM v0.31.1 path)

Four backend instances total. New AM/Mimir releases get added to the ceiling slot (and the prior ceiling drops out) on a pull-when-needed basis.

**Implication.** Backend audit §1.1 is updated to reflect the new vanilla floor. Mimir audit gets a short floor/ceiling note.

---

## O. Licensing

### O1. Project license and contribution conventions

**Context.** The project will be open-sourced on GitHub under the maintainer's personal namespace. Licensing has to be settled before the first push because the LICENSE in the initial commit sets contributor expectations and is hard to change retroactively. Audit of pulled-in deps (bubbletea/bubbles/lipgloss/huh/glamour MIT, cobra Apache 2.0, yaml.v3 Apache+MIT, fsnotify BSD-3, alertmanager API models Apache 2.0) — all permissive, no copyleft. Mimir is AGPL-3.0 but is reached only over HTTP, so it does not propagate.

**Blocks.** First push to GitHub, contribution policy, scaffold PR (must include LICENSE + SPDX headers from commit one).

**Status.** Resolved 2026-04-25.

- **License**: **Apache License 2.0**. Matches Alertmanager (the integration target) and k9s (the inspiration), is the cloud-native ecosystem default, and ships with an explicit patent grant. All deps are compatible.
- **`LICENSE` file** at repo root — standard Apache 2.0 text verbatim. Already in place (copied from Alertmanager's verified LICENSE; the boilerplate appendix is part of the license text and stays as-is).
- **`NOTICE` file** — **not** required. We do not vendor Apache-licensed third-party code; Go modules link rather than embed source. Skip.
- **Per-file headers** — full Apache header **skipped** to keep file diffs noise-free (cobra and many other Go projects do the same). Instead, every Go source file carries a single-line SPDX identifier as the very first line:

  ```go
  // SPDX-License-Identifier: Apache-2.0
  ```

  Machine-readable, two characters of overhead per file, and tools like `reuse-tool` and GitHub's licensee recognise it.
- **Copyright line in source files** — not required (Apache 2.0 explicitly permits omission when the LICENSE is in the repo); skipped to reduce diff noise. The LICENSE is the authoritative copyright notice.
- **README badge** — `![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)` linking to `LICENSE`.
- **Contributor policy: DCO**, not CLA. Contributors sign off commits with `git commit -s` (adds a `Signed-off-by:` trailer). A short `CONTRIBUTING.md` explains the requirement; CI optionally enforces it via the DCO GitHub App later. CLAs are overhead a personal-namespace project does not need.
- **Mimir AGPL note** — explicitly documented (here and in backend audit §2): we do not import Mimir code. If we ever do, the licensing question reopens, since AGPL would either force a relicense or require keeping the imported code in a separate process / boundary. Not a v0.1 concern.

---

## P. Release engineering

### P1. Release toolchain (goreleaser + GitHub Actions)

**Context.** Once v0.1 is feature-complete we need a reproducible release pipeline. k9s uses [goreleaser](https://goreleaser.com) for cross-platform builds, archives, checksums, deb/rpm/apk packages, and SBOMs — but their `.goreleaser.yml` is run manually on tag push (no GitHub Actions workflow). Modern personal-namespace projects wire goreleaser into Actions so a `git tag` is the only manual step.

**Blocks.** First tagged release of v0.1.

**Status.** Resolved 2026-04-25.

- **Tool**: goreleaser v2 (`version: 2` config, pinned via `goreleaser-action@v6` with `version: "~> v2"`).
- **Trigger**: GitHub Actions workflow `.github/workflows/release.yml`, fired on `git push --tags` for tags matching `v*`. Tagging from the maintainer's local checkout is the only manual step; everything downstream is automated.
- **Build matrix** (mirrors k9s minus the niche archs): Linux amd64/arm64/arm-v7, FreeBSD amd64/arm64, macOS amd64/arm64 (combined into a universal binary), Windows amd64/arm64. ppc64le and s390x dropped — easy to add if asked.
- **Artifacts**: per-arch archives (zip on Windows, tar.gz elsewhere), `checksums.sha256`, deb/rpm/apk packages via `nfpms`, SBOMs per archive via `sboms`.
- **Version injection**: `-X github.com/wilfriedroset/a10r/cmd.version={{.Version}}` and matching `commit`/`date` ldflags. Scaffold must expose package-level `var version, commit, date string` in `cmd/` so the `version` and `info` subcommands (per K1) print real values.
- **Prerelease detection**: `release.prerelease: auto` — tags like `v0.1.0-rc.1` are auto-flagged as prerelease on the GitHub release page.
- **Changelog**: built from commit messages between tags. Filters drop `docs:`, `test:`, `chore:`, `ci:` prefixes and merge-PR noise.
- **Homepage**: `https://github.com/wilfriedroset/a10r` (the repo) is used in nfpms metadata and any future Homebrew formula. The maintainer has no separate site; the repo is canonical.

**Deferred / TODO when relevant:**

- **Homebrew tap** (`brews:` block). Requires creating an empty `wilfriedroset/homebrew-tap` repo; goreleaser pushes the formula there on each release. Not in v0.1 to avoid the first release failing on a missing tap. Add when there is real demand for `brew install`.
- **Docker image** (`dockers:` block). A TUI is rarely run in a container, but it's useful for jumphost workflows. Add post-v0.1 if asked. Image would publish to `ghcr.io/wilfriedroset/a10r`.
- **Cosign signing** of binaries and SBOMs. Add once we have a release cadence and a real audience that cares about signature verification.
- **Test/lint CI**. A separate workflow (PR-triggered) running `prek run --all-files` and `go test ./...` is part of the scaffold PR, not P1.

**Tagging convention**: semver, `v` prefix. `v0.1.0` for the MVP; `v0.1.0-rc.1` for release candidates.
