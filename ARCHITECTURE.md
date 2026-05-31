# Architecture

a10r is a single Go binary: a k9s-shaped terminal UI for Alertmanager
and Grafana Mimir, plus a set of headless list subcommands. The TUI is
built on Bubble Tea v2's Model/Update/View loop -- one root
`tea.Model` (`app.App`) frames the screen and routes messages to a
stack of pages; see [ADR 0042](docs/adr/0042-bubbletea-over-tview.md)
for why Bubble Tea over tview.

This document is the connecting tissue between
[CONTEXT.md](CONTEXT.md) -- the domain glossary -- and
[docs/adr/](docs/adr/) -- the point decisions. It maps the package
layout and walks two end-to-end paths: how a page is born, and how a
backend call travels from a page to the wire. It links out to ADRs by
number for the "why"; it does not restate their rationale. Domain
terms (Alert, alert instance, silence-all, error band, top panel, ...)
are defined in CONTEXT.md and used here without redefinition.

## Package layout

### Entry points

- `cmd/` -- the cobra CLI. `cmd/root.go` builds the root command;
  `cmd/cmd.go` registers subcommands (`version`, `info`, `validate`,
  `doctor`, `init`, `alerts`, `silences`, `groups`, `receivers`).
  Running `a10r` with no subcommand drops into the TUI via
  `cmd/tui.go`'s `runTUI`. The four list subcommands
  (`cmd/alerts.go`, `cmd/silences.go`, `cmd/groups.go`,
  `cmd/receivers.go`) are thin shells over `internal/listcmd`.
- `cmd/smoke/` -- a manual integration harness (`Command smoke`), not
  part of the shipped binary.

### Backend (`internal/backend`)

- `internal/backend` -- the wire-facing domain types
  (`Alert`, `AlertGroup`, `Silence`, `Receiver`, `Status`, ...), the
  sentinel errors, and the `Client` interface every backend
  satisfies. `Client` composes `Reader` (six read methods), `Writer`
  (three non-idempotent silence mutations), capability-gated config
  methods, and `Capabilities()`. See
  [ADR 0028](docs/adr/0028-backend-client-surface.md).
- `internal/backend/vanilla` -- the one real implementation of
  `Client`, talking to the upstream Alertmanager v2 HTTP API. Read
  endpoints live in `vanilla/read.go`, writes in `vanilla/write.go`,
  conversion in `vanilla/convert.go`, capability stubs in
  `vanilla/stubs.go`. This is the source of truth for "talk to
  `/api/v2/...`".
- `internal/backend/mimir` -- a constructor only. Mimir is a
  `vanilla.Client` with a URL prefix (`/alertmanager`) and a tenant
  header (`X-Scope-OrgID`); it adds no behaviour, so `mimir.New`
  returns `*vanilla.Client`. See
  [ADR 0044](docs/adr/0044-mimir-config-mirrors-remote-write.md) for
  the config-mirrors-`remote_write` shape.
- `internal/backend/factory` -- the single wiring point between one
  `a10r.yaml` `backends:` entry and a constructed `Client`. There is
  no `NewVanilla` / `NewMimir` split: `factory.Build` always calls
  `mimir.New`, with vanilla being the empty-prefix, no-headers case
  (ADR 0028). It lives as a sub-package to avoid a
  `backend -> mimir -> backend` import cycle.
- `internal/backend/transport` -- composes the `http.RoundTripper`
  layers (auth, header injection, User-Agent, TLS, proxy, debug
  logging) and the host-pinning that defends against credential
  replay on cross-origin redirects. mTLS / OAuth2 / SigV4 are
  reserved per [ADR 0029](docs/adr/0029-tls-cert-key-reserved.md).
- `internal/backend/multi` -- the multi-tenant fan-out. A
  `multi.Client` is deliberately NOT a `backend.Client`: its read
  methods return `[]Result[V]` (one per tenant, errors surfaced
  never swallowed) under a bounded worker pool.
- `internal/backend/tls` -- a standalone TLS-handshake helper for
  `a10r doctor`'s cert-expiry check, kept separate from `transport`
  so the probe never reaches into a constructed client's opaque
  RoundTripper chain.
- `internal/backend/backendtest` -- shared test stubs for the
  `Client` surface.

### Config and CLI support (`internal/`)

- `internal/config` -- the in-memory shape of `a10r.yaml`, env-var
  interpolation, and CLI/env/config/default precedence resolution.
  The schema mirrors Prometheus's `remote_write` block.
- `internal/doctor` -- preflight health checks against every
  configured backend, each a small `Checker` (reachability, auth,
  version floor, TLS expiry, mount probe, ...).
- `internal/listcmd` -- the shared orchestrator for the four headless
  list commands: per-backend fan-out, lenient partial-failure,
  TTY-vs-pipe format resolution, pager lifecycle, deterministic
  ordering. Generic over the row type.
- `internal/wizard` -- a line-oriented interactive-prompt helper for
  `a10r init`, deliberately not Bubble Tea (init runs before the TUI
  exists).
- `internal/matcher` -- parses Prometheus-style label matchers
  (`name<op>value`, the four operators `=` `!=` `=~` `!~`) used by
  `--matcher` flags, silence forms, and label selectors.
- `internal/output` -- generic table / json / yaml encoders for the
  read-only command results.
- `internal/clock` -- the time-injection seam keeping tests off the
  wall clock (ADR 0031).
- `internal/log` -- builds the project `*slog.Logger` (json / logfmt,
  no ANSI).
- `internal/xdg` -- env-var slot names and the Windows fallback for
  OS-conformant path resolution.

### TUI (`internal/tui`)

Shell and orchestration:

- `internal/tui/app` -- the root `tea.Model` (`app.App`). Owns the
  page stack, the two body-slot overlays, the footer, the app-global
  time / state-breakdown toggles, and the poll-data / backend-status
  caches replayed into a freshly pushed page. App private state is
  split into named sub-structs per
  [ADR 0032](docs/adr/0032-app-state-substructs.md).
- `internal/tui/boot` -- the startup orchestrator
  ([ADR 0023](docs/adr/0023-tui-boot.md)): parse config, build the
  logger, build backend clients, fetch tenant versions, load the
  skin, register key chords / aliases, build the page-environment
  resolver and the `App`. `boot.Build` reads top-to-bottom as a
  named-stage list ([ADR 0033](docs/adr/0033-boot-stage-extraction.md)).
- `internal/tui/keys` -- the keybindings dispatcher: five precedence
  layers (modal > prompt > per-view > table-context > global), first
  match wins, 500 ms chords.
- `internal/tui/action` -- the leaf `Action` keybinding-metadata type
  pages emit from `Bindings()`, plus the read-only filter; UI-free by
  design (ADR 0019).
- `internal/tui/poll` -- one (backend, resource) poll loop with
  backoff, jitter, and asymmetric connection-state emission
  (ADR 0014).

Pages and shared page bases (`internal/tui/page`):

- `page/listpage` -- the shared base for list-style pages. `Base`
  holds the type-independent fields (cursor window, filter, scope,
  pause, backend health, recompute callbacks) every list page embeds;
  it is NOT a `tea.Model`. See
  [ADR 0013](docs/adr/0013-list-page-shared-base.md). `Base` owns the
  wire-to-domain seam for sideband messages and `DataMsg`
  ([ADR 0018](docs/adr/0018-listpage-wire-to-domain-seam.md)).
- `page/detailpage` -- the shared 1D-scroll base for the read-only
  detail pages, embedded the same explicit way
  ([ADR 0022](docs/adr/0022-detailpage-shared-base.md)).
- `page/cursor` -- the cursor / scroll primitives (`Window` for
  tables, half/full-page steps for viewers), keeping reconcile-on-
  change a type property (ADR 0016).
- `page/alerts` -- the home page: the L1 alerts list, rowing on the
  alertname aggregate ([ADR 0040](docs/adr/0040-alerts-page-aggregates-by-alertname.md)).
- `page/groupdetail` -- the L2 group-detail instance list.
- `page/alert` -- the L3 read-only instance-detail view.
- `page/silences`, `page/silence` -- the silences list and the
  read-only silence-detail view.
- `page/groups` -- the route-based alert-groups tree.
- `page/receivers` -- the receivers list (Enter drills to a filtered
  alerts page).
- `page/status` -- the Alertmanager status pane.
- `page/tenant`, `page/tenantconfig` -- the configured-backend table
  and the per-tenant config inspector.
- `page/format` -- width-aware text helpers (cell padding,
  cell-counting truncation) shared across pages and chrome.
- `page/pagetest` -- the shared page-test harness (ADR 0026).

Chrome, overlays, and rendering helpers:

- `internal/tui/panel` -- the k9s-style top panel (tenant shortcuts,
  hint grid, ASCII logo) and the bordered body wrapper.
- `internal/tui/header` -- the three-zone single-line header strip
  used by non-top-panel surfaces.
- `internal/tui/footer` -- the bottom strip: crumbs, prompt, flash,
  and the optional rotating hint bar.
- `internal/tui/modal` -- the async-result overlays (tenant picker,
  yes/no confirm); viewer overlays live in `help` (ADR 0020).
- `internal/tui/help` -- the `?` help overlay.
- `internal/tui/cmdbar` -- resolves `:` command strings to
  `tea.Cmd`s by exact-or-unique-prefix match.
- `internal/tui/form/silence` -- the silence create / edit form
  (ADR 0025).
- `internal/tui/edit` -- hands a buffer to the user's external editor
  via `tea.ExecProcess`.
- `internal/tui/bulkop` -- the per-tenant fan-out shared by the
  alerts bulk-silence and silences bulk-expire flows.
- `internal/tui/browser` -- a dumb default-browser launcher.
- `internal/tui/tablesort` -- the shared `Shift+<letter>` sort-state
  machine for table pages.
- `internal/tui/stateformat` -- the app-global full/compact
  state-breakdown toggle.
- `internal/tui/timerender` -- the four CONTEXT.md time vocabularies
  (relative, absolute, remaining, next attempt) plus a `Duration`
  primitive (ADR 0015).
- `internal/tui/theme` -- parses k9s-format skins into a `Styles`
  struct consumed by role name (ADR 0030).
- `internal/tui/yamlstyle` -- applies skin YAML roles to a YAML body.

## Birth of a TUI page

A page is constructed by a factory closure, pushed onto the App's
stack inside the Update loop, and rendered by `View`. The path from
the binary entry to a live page:

1. `cmd/tui.go`'s `runTUI` is the cobra `RunE`. It is intentionally
   tiny: it calls `boot.Build(ctx, flags, deps)` to assemble the
   startup graph, then wraps the returned `App` in
   `tea.NewProgram` with the quit filter.

2. `internal/tui/boot/boot.go`'s `Build` runs the startup stages
   ([ADR 0023](docs/adr/0023-tui-boot.md),
   [ADR 0033](docs/adr/0033-boot-stage-extraction.md)): resolve the
   effective config, install the logger, build the per-backend
   `backend.Client` map via the factory, fetch tenant versions,
   resolve the config dir and load the skin, build the key
   dispatcher, build the `pageEnv` plus the cmdbar resolver, and
   construct the `App`. `Build` deliberately does NOT start the
   program or push a page -- both need `prog.Send`, which does not
   exist until `tea.NewProgram` returns. It returns a `Result`
   bundling the `App`, the poller registry, and the `pageEnv`.

3. Back in `runTUI`, after the program is built,
   `res.StartPollers(ctx, prog.Send)` spawns the poll goroutines and
   `res.PushHome(ctx, prog.Send)` enqueues the home page. `PushHome`
   builds the home factory -- `func() app.Page { return
   newAlertsPage(env, "", "") }` -- wraps it in `app.PushPage`, and
   sends the resulting `pushPageMsg` into the loop.

4. `internal/tui/boot/page_factory.go` holds the `pageEnv` struct and
   the `newXxxPage` factories. `pageEnv` bundles the shared deps every
   page needs at construction time (styles, scope, clients, the
   time/state-format closures that read the live `App`, the editor
   resolver, tenant rows, ...) so adding a future shared dep is a
   struct-field change, not an N-arg propagation. `newAlertsPage`
   translates the `pageEnv` into `alerts.Options` and calls
   `alerts.New`. The cmdbar resolver registers the other factories
   (`newSilencesPage`, `newGroupsPage`, ...) as `:command` handlers
   that close over the same `env`.

5. `app.PushPage(factory)` returns a `tea.Cmd` producing a
   `pushPageMsg{Factory}`. The factory shape (not a `Page` value) is
   load-bearing: it lets the page's `Init` run inside the App's Update
   cycle so the returned `Cmd` reaches the program loop.
   `internal/tui/app/lifecycle.go`'s `pushPage` appends the page,
   batches its `Init` with cached-snapshot replay
   (`replayCachedDataMsgs`), and -- for a `PollAwarePage` -- filters
   the replay to the page's declared resources so it hydrates without
   waiting for the next poll tick.

6. The page itself embeds `listpage.Base`
   ([ADR 0013](docs/adr/0013-list-page-shared-base.md)). `alerts.New`
   constructs the page value and initialises its `Base`, wiring the
   `Recompute`, `RowCount`, `SnapshotFocus`, `SetTimeFormat`,
   `SetStateFormat`, and `ClearMarks` callbacks. From then on the
   App routes messages to the top page's `Update`, the page delegates
   sideband and `DataMsg` handling into `Base`
   ([ADR 0018](docs/adr/0018-listpage-wire-to-domain-seam.md)), and
   `View(width, height)` renders rows inside the panel chrome.
   Detail pages (`page/alert`, `page/silence`, `page/tenantconfig`)
   embed `detailpage.Base` instead
   ([ADR 0022](docs/adr/0022-detailpage-shared-base.md)).

The page stack is `app.App.stack`: index 0 is home, the last element
is the active top-of-stack. `app.PopPage` / `app.ReplacePage` are the
other two stack transitions; each runs the departing page's `Close`
exactly once.

## Birth of a backend call

A backend call starts at a page (or a poll loop), goes through the
`backend.Client` interface, into the `vanilla.Client` HTTP methods,
and down the `transport` RoundTripper stack to the wire.

1. A page holds backend clients via its `Options` (the silence-write
   clients) and the App's poll loops hold the read clients. Both come
   from the same per-backend `map[string]backend.Client` that
   `boot.Build` constructs. A page never branches on backend type: it
   reads `Client.Capabilities()` before offering a capability-gated
   action ([ADR 0028](docs/adr/0028-backend-client-surface.md)).

2. The interface boundary is `internal/backend/client.go`. `Reader`
   is the read-only subset (six methods) so test fakes stay small;
   `Writer` is the three non-idempotent silence mutations, which the
   backoff loop must never auto-retry. `Client` unifies both plus the
   capability-gated config methods. The interface deliberately does
   not expose backend type.

3. The concrete implementation is `internal/backend/vanilla/client.go`.
   `vanilla.New` validates the `BaseURL` eagerly, folds the optional
   `Prefix` into `base` (`BaseURL + Prefix`), and builds the
   `*http.Client` with the supplied transport, a per-request timeout,
   and a `CheckRedirect` that refuses cross-origin redirects when
   `ExpectedHost` is set. Read methods build a URL via `urlFor`, then
   run `doGet` -> `exec`, which classifies the HTTP status into the
   `backend` sentinels (`ErrUnreachable`, `ErrUnauthorized`,
   `ErrNotFound`, a retryable `transientError` for 5xx/429) and
   decodes a body-size-capped JSON response, sanitising any
   server-controlled error body before it lands in an error string.

4. The transport stack is composed in `mimir.New` (the constructor
   the factory always calls) and lives in
   `internal/backend/transport/transport.go`. The RoundTripper chain,
   innermost first:

   - `NewBase` -- the `*http.Transport` carrying TLS and proxy
     config, or `http.DefaultTransport` when neither is set.
   - `WithDebugLog` -- per-request debug logging, active only with
     `--debug-http` (ADR 0008 owns the header redaction).
   - `NewAuth` -- at most one of basic / authorization / bearer
     (matching Prometheus's `HTTPClientConfig`).
   - `WithHostPinnedHeaders` -- arbitrary headers plus the tenant
     header the factory folded in.
   - `WithUserAgent` -- the a10r identifier, injected on every hop
     including redirects (it is not a secret).

   Host pinning is the security backbone here, implemented in
   `transport` rather than an ADR of its own. `AuthOptions.ExpectedHost`
   (the host parsed from `BaseURL` by `mimir.parseExpectedHost`) is
   threaded into every credential- and header-injecting layer. The
   shared `mutatingRT` clones each request and applies its mutation
   only when `req.URL.Host` matches `expectedHost`, so a hijacked
   backend returning a `302` to `https://attacker/` cannot replay the
   `Authorization` or tenant headers at the redirect target. The
   vanilla `CheckRedirect` is belt-and-braces: it turns the
   cross-origin redirect itself into a loud transport error
   (`ErrCrossOriginRedirect`) rather than letting it silently follow.
   TLS cert/key fields are reserved for future mTLS
   ([ADR 0029](docs/adr/0029-tls-cert-key-reserved.md)).

5. Multi-tenant fan-out is `internal/backend/multi`. When the scope is
   "all" or a subset, a `multi.Client` runs the same `Reader` call
   against every per-tenant `backend.Client` in parallel under a
   bounded pool and returns `[]Result[V]` -- one entry per tenant in
   declaration order, with per-tenant errors on `Result.Err` so the
   error band can name the offender. Single-tenant operations
   (`GetSilence`, the write methods, capability methods) stay on the
   bare `backend.Client`; the caller picks the tenant.
