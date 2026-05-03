// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/page/alerts"
	"github.com/wilfriedroset/a10r/internal/tui/page/groups"
	"github.com/wilfriedroset/a10r/internal/tui/page/receivers"
	"github.com/wilfriedroset/a10r/internal/tui/page/silences"
	"github.com/wilfriedroset/a10r/internal/tui/page/status"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenant"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenantconfig"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// runTUI assembles the bubbletea program and runs it. Called from
// the root command's RunE when no subcommand is supplied.
//
// v0.1 wiring is deliberately minimal: load config, build the
// first backend via the factory, push an alerts page as the home
// view, kick a poller for the alerts resource. Tenant fan-out,
// multi-backend pollers, and the silence-form / editor /
// receivers / groups / tenant pages are reachable via the `:`
// command bar — they just stay empty until the user navigates.
func runTUI(cmd *cobra.Command, flags *GlobalFlags) error {
	cfg, err := loadConfigForTUI(flags)
	if err != nil {
		return err
	}

	styles, err := loadStylesFor(cfg.Theme.Name)
	if err != nil {
		return err
	}

	registry := action.New()
	dispatcher := keys.New(nil)
	scope := scopeFor(cfg)
	clients := buildClients(cfg)
	silenceClients := silenceClientsFrom(clients)
	silenceWriteClients := silenceWriteClientsFrom(clients)
	creator := os.Getenv("USER")

	// Fetch each backend's Alertmanager version once at startup
	// so the tenant page can render a VERSION column without a
	// separate per-(backend, status) poller. Failures leave the
	// version blank; the page renders "—" so the column stays
	// aligned. Per Q4.2 — the version changes rarely, the cost of
	// a separate poller isn't justified.
	tenantVersions := fetchTenantVersions(cmd.Context(), clients)
	tenantRows := buildTenantRows(cfg, tenantVersions)
	// The resolver's page factories close over `a` so the active
	// app-global TimeFormat reaches each newly-pushed page (issue
	// raised in the post-batch review: pages pushed *after* the
	// user toggled `t` were opening in relative). a is forward-
	// declared and assigned on the line below; closures read it at
	// invocation time, by which point app.NewApp has run.
	var a *app.App
	timeFormat := func() app.TimeFormat {
		if a == nil {
			return app.TimeFormatRelative
		}
		return a.TimeFormat()
	}
	resolver := newResolver(*styles, scope, silenceClients, silenceWriteClients, creator,
		tenantRows, cfg, clients, timeFormat)

	// `gg` is a chord — the dispatcher buffers the first `g` and
	// fires the registered handler on the second within 500 ms.
	// Registering at LayerTable means every table-bodied page
	// (alerts, silences, receivers, groups, tenant) honours it
	// without per-page chord plumbing.
	dispatcher.Set(keys.LayerTable, "gg", func() tea.Cmd {
		return func() tea.Msg { return app.GoToFirstRowMsg{} }
	})

	// pollerReg is mutated after the pollers spawn, but the
	// closure handed to the App captures a pointer so the empty
	// slice we publish here is filled before the user can press
	// `r`. (NewProgram needs the model up front in bubbletea v2;
	// the model needs a refresh handler; the handler needs the
	// pollers; the pollers need prog.Send — so something has to
	// be deferred. We defer the membership of the slice rather
	// than the wiring shape.)
	pollerReg := &pollerRegistry{}
	a = app.NewApp(app.Options{
		Styles:     *styles,
		Registry:   registry,
		Dispatcher: dispatcher,
		CmdBar:     resolver,
		Tenants:    backendNames(cfg),
		Refresh:    pollerReg.Refresh,
	})

	prog := tea.NewProgram(a, tea.WithContext(cmd.Context()))

	// Spawn the poller for every configured backend and publish
	// each into the registry so the refresh handler can find them.
	stopPoller := startBackendPoller(cmd.Context(), cfg, clients, prog, pollerReg)
	defer stopPoller()

	// Push the alerts home page once the program is running. The
	// app.PushPage Cmd is invoked once to extract its message,
	// which Send routes through the Update loop on the next tick.
	go func() {
		homeFactory := func() app.Page {
			return alerts.New(alerts.Options{
				Styles:     *styles,
				Now:        time.Now,
				Scope:      scope,
				Clients:    silenceClients,
				Creator:    creator,
				TimeFormat: timeFormat(),
			})
		}
		prog.Send(app.PushPage(homeFactory)())
	}()

	_, err = prog.Run()
	return err
}

// silenceClientsFrom narrows the backend.Client map to the small
// silenceform.Client interface — keeps the form package free of
// the wider Client surface and makes tests trivial to fake.
func silenceClientsFrom(in map[string]backend.Client) map[string]silenceform.Client {
	out := make(map[string]silenceform.Client, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// silenceWriteClientsFrom narrows the backend.Client map to the
// silences page's Client interface — that's silenceform.Client
// plus ExpireSilence, which the silences page needs for `x` /
// `Ctrl+X`. Separate from silenceClientsFrom because the alerts /
// alert / groups pages don't expire silences and shouldn't pull
// the wider surface in.
func silenceWriteClientsFrom(in map[string]backend.Client) map[string]silences.Client {
	out := make(map[string]silences.Client, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// buildClients constructs one backend.Client per configured backend
// keyed by tenant name. A backend whose factory.Build fails logs a
// warning and is skipped — the rest still get a client. The
// resulting map is shared between the poller fan-out (read paths)
// and the page factories (write paths) so the two stay in sync.
//
// The User-Agent is identical for every backend per RFC 9110 §10.1.5
// — backends differentiate via the existing tenant header, so a
// per-backend UA would only add noise to backend access logs.
func buildClients(cfg *config.Config) map[string]backend.Client {
	ua := userAgent(version, commit)
	out := make(map[string]backend.Client, len(cfg.Backends))
	for _, be := range cfg.Backends {
		c, err := factory.Build(be, ua)
		if err != nil {
			fmt.Fprintf(os.Stderr, "backend %q: build failed: %v\n", be.Name, err)
			continue
		}
		out[be.Name] = c
	}
	return out
}

// userAgent returns the RFC 9110 User-Agent string identifying this
// build of a10r. Format: `a10r/<ver>` for plain releases,
// `a10r/<ver> (<comm>)` when a non-default commit is available —
// gives backend operators one grep-able token while keeping the
// header short for log aggregators. The build vars are injected at
// link time by goreleaser and default to "dev"/"none" for local
// builds; tests pass them explicitly so the function does not read
// package state and remains data-race free under t.Parallel.
func userAgent(ver, comm string) string {
	if comm == "" || comm == "none" {
		return "a10r/" + ver
	}
	return "a10r/" + ver + " (" + comm + ")"
}

// loadConfigForTUI loads the user config; missing config returns
// a zero Config so the program still starts (the wizard wires
// from there in a future commit).
func loadConfigForTUI(flags *GlobalFlags) (*config.Config, error) {
	cfg, err := config.Load(loadOptsFromFlags(flags))
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			fmt.Fprintln(os.Stderr, "no config found — starting with empty backend list (run `a10r validate` after editing your config)")
			return &config.Config{}, nil
		}
		return nil, err
	}
	return cfg, nil
}

// loadOptsFromFlags translates persistent flags into config.LoadOpts.
// --config (a file path) splits into Dir + File so the loader reads
// the requested file directly; --config-dir falls back to the XDG
// resolution path with the canonical "a10r.yaml" basename.
func loadOptsFromFlags(flags *GlobalFlags) config.LoadOpts {
	if flags.ConfigPath != "" {
		return config.LoadOpts{
			Dir:  filepath.Dir(flags.ConfigPath),
			File: filepath.Base(flags.ConfigPath),
		}
	}
	return config.LoadOpts{Dir: flags.ConfigDir}
}

// backendNames returns the configured tenant names in
// configuration order. Used to populate the panel's tenant-
// shortcut column.
func backendNames(cfg *config.Config) []string {
	out := make([]string, len(cfg.Backends))
	for i, b := range cfg.Backends {
		out[i] = b.Name
	}
	return out
}

// fetchTenantVersions issues one /api/v2/status call per
// configured backend and returns the resolved Alertmanager
// version keyed by backend name. Concurrent fan-out so a slow
// backend doesn't block startup; per-backend timeout caps each
// call so a hung backend doesn't stall the program. Failures
// silently produce an empty entry — the tenant page renders "—"
// for missing versions per Q4.2.
func fetchTenantVersions(ctx context.Context, clients map[string]backend.Client) map[string]string {
	out := make(map[string]string, len(clients))
	if len(clients) == 0 {
		return out
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for name, client := range clients {
		wg.Add(1)
		go func(name string, c backend.Client) {
			defer wg.Done()
			fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			st, err := c.Status(fctx)
			if err != nil {
				return
			}
			mu.Lock()
			out[name] = st.Version.Version
			mu.Unlock()
		}(name, client)
	}
	wg.Wait()
	return out
}

// buildTenantRows assembles the tenant page's row list from
// configured backends + the startup-fetched version map.
// Backends whose factory.Build failed are still surfaced (the
// user wants to see the misconfigured entry in the tenant table)
// but with an empty version that renders as "—".
func buildTenantRows(cfg *config.Config, versions map[string]string) []tenant.Row {
	rows := make([]tenant.Row, 0, len(cfg.Backends))
	for _, be := range cfg.Backends {
		rows = append(rows, tenant.Row{
			Name:    be.Name,
			URL:     be.URL,
			Version: versions[be.Name],
			// Conn / Alerts / Silence stay zero — the wiring layer
			// no longer feeds live counts into this page since the
			// tenant table is read-only as of #7. A future commit
			// can re-attach poll snapshots if the columns become
			// load-bearing again.
		})
	}
	return rows
}

// tenantConfigIndex returns a map from backend name to its
// resolved config.Backend struct so the tenant-config drill
// factory can hand the right entry to the inspector page.
func tenantConfigIndex(cfg *config.Config) map[string]config.Backend {
	out := make(map[string]config.Backend, len(cfg.Backends))
	for _, be := range cfg.Backends {
		out[be.Name] = be
	}
	return out
}

// scopeFor returns the tenant label rendered in the alerts page
// title. Single backend → its name; two or more → "all" (the
// k9s convention for the multi-namespace case). Empty config →
// "all" so the title still reads cleanly even pre-wizard.
func scopeFor(cfg *config.Config) string {
	switch len(cfg.Backends) {
	case 0:
		return "all"
	case 1:
		return cfg.Backends[0].Name
	default:
		return "all"
	}
}

// loadStylesFor compiles the requested theme. Empty falls back to
// the default skin name.
func loadStylesFor(name string) (*theme.Styles, error) {
	if name == "" {
		name = theme.DefaultSkinName
	}
	return (&theme.Loader{}).Load(name)
}

// newResolver builds the cmdbar resolver with the v0.1 alias
// catalogue. Page factories close over the styles + scope + the
// per-tenant client map so each `:alerts` / `:silences` push
// lands a page wired to the active tenant label and (for write-
// surface pages) the right backend.Client when the user invokes
// a write action.
func newResolver(
	styles theme.Styles,
	scope string,
	silenceClients map[string]silenceform.Client,
	silenceWriteClients map[string]silences.Client,
	creator string,
	tenantRows []tenant.Row,
	cfg *config.Config,
	clients map[string]backend.Client,
	timeFormat func() app.TimeFormat,
) *cmdbar.Resolver {
	r := cmdbar.New()
	r.Register("alerts", func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page {
			return alerts.New(alerts.Options{
				Styles:     styles,
				Now:        time.Now,
				Scope:      scope,
				Clients:    silenceClients,
				Creator:    creator,
				TimeFormat: timeFormat(),
			})
		})
	})
	editorResolver := edit.SystemResolver()
	silencesFactory := func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page {
			return silences.New(silences.Options{
				Styles:          styles,
				Now:             time.Now,
				Clients:         silenceWriteClients,
				Creator:         creator,
				EditorResolver:  editorResolver,
				TimeFormat:      timeFormat(),
				BulkConcurrency: cfg.Defaults.BulkConcurrencyOrDefault(),
				Logger:          slog.Default(),
			})
		})
	}
	r.Register("silences", silencesFactory)
	r.Register("sil", silencesFactory)
	r.Register("status", func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return status.New(styles, scope) })
	})
	receiversFactory := func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return receivers.New(styles) })
	}
	r.Register("receivers", receiversFactory)
	r.Register("rec", receiversFactory)
	groupsFactory := func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page {
			return groups.New(groups.Options{
				Styles:  styles,
				Now:     time.Now,
				Clients: silenceClients,
				Creator: creator,
			})
		})
	}
	r.Register("groups", groupsFactory)
	r.Register("gr", groupsFactory)
	tenantConfigByName := tenantConfigIndex(cfg)
	drillFactory := func(name string) (app.Page, error) {
		be, ok := tenantConfigByName[name]
		if !ok {
			return nil, fmt.Errorf("backend %q not in config", name)
		}
		fetcher, ok := clients[name]
		if !ok {
			return nil, fmt.Errorf("backend %q failed to build at startup — fix a10r.yaml and restart", name)
		}
		return tenantconfig.New(tenantconfig.Options{
			Tenant:  name,
			Backend: be,
			Fetcher: fetcher,
			Styles:  styles,
		}), nil
	}
	tenantFactory := func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page {
			p := tenant.New(tenant.Options{
				Styles:       styles,
				DrillFactory: drillFactory,
			})
			p.SetRows(tenantRows)
			return p
		})
	}
	r.Register("tenant", tenantFactory)
	r.Register("tenants", tenantFactory)
	r.Register("q", func(_ []string) tea.Cmd { return tea.Quit })
	return r
}

// startBackendPoller spawns the per-(backend, resource) poller
// matrix per audit §5.1. Each entry in clients gets one poller
// per resource (alerts, silences, receivers, alert-groups), and
// every emitted DataMsg carries the backend's tenant tag so list
// pages can union snapshots into a `byTenant` map and reason
// about scope at render time.
//
// The four resources share a single interval per backend: poll
// pressure is dominated by the alerts feed, and the others are
// cheap reads that piggy-back. Configurable per-resource intervals
// are deferred — overkill for v0.1 and not in the audit.
//
// reg is published with each poller so the App's `r` refresh
// handler can find the matching entry by (resource, tenant).
func startBackendPoller(ctx context.Context, cfg *config.Config, clients map[string]backend.Client, prog *tea.Program, reg *pollerRegistry) func() {
	if len(clients) == 0 {
		return func() {}
	}
	pollers := make([]*poll.Poller, 0, len(clients)*4)
	for _, be := range cfg.Backends {
		c, ok := clients[be.Name]
		if !ok {
			continue // factory.Build failed in buildClients; warning already emitted
		}
		interval := backendInterval(be, cfg)
		name := be.Name
		for _, entry := range backendFetchers(c) {
			p := poll.New(poll.Options{
				Tenant:   name,
				Resource: entry.resource,
				Interval: interval,
				Fetch:    entry.fetch,
				Send:     prog.Send,
			})
			p.Start(ctx)
			pollers = append(pollers, p)
			reg.Add(p)
		}
	}
	return func() {
		for _, p := range pollers {
			p.Stop()
		}
	}
}

// backendInterval picks the active poll interval for a backend.
// Per-backend `poll_interval` wins; falls back to the global
// default; ultimate fallback is 1 minute (audit §5.1, I3).
func backendInterval(be config.Backend, cfg *config.Config) time.Duration {
	if be.PollInterval > 0 {
		return be.PollInterval
	}
	if cfg.Defaults.PollInterval > 0 {
		return cfg.Defaults.PollInterval
	}
	return time.Minute
}

// fetcherEntry pairs a poll-resource label with its fetch func.
// The label feeds poll.Options.Resource so the refresh registry
// can route an `r` press to the right poller — without it the
// loop is anonymous and every press would have to re-poll every
// resource.
type fetcherEntry struct {
	resource string
	fetch    func(ctx context.Context) (any, error)
}

// backendFetchers returns the four poller fetch funcs for one
// backend client — alerts, silences, receivers, alert-groups.
// Each returns the resource as `any` so poll.Options.Fetch can
// be a single shape across resource types. The resource labels
// must match the strings the pages emit on RefreshRequestedMsg
// ("alerts", "silences", "receivers", "groups").
func backendFetchers(c backend.Client) []fetcherEntry {
	return []fetcherEntry{
		{resource: "alerts", fetch: func(ctx context.Context) (any, error) {
			return c.ListAlerts(ctx, backend.AlertFilter{})
		}},
		{resource: "silences", fetch: func(ctx context.Context) (any, error) {
			return c.ListSilences(ctx, backend.SilenceFilter{})
		}},
		{resource: "receivers", fetch: func(ctx context.Context) (any, error) {
			return c.ListReceivers(ctx)
		}},
		{resource: "groups", fetch: func(ctx context.Context) (any, error) {
			return c.ListAlertGroups(ctx, backend.AlertFilter{})
		}},
	}
}

// pollerRegistry is the wiring-layer index the App's `r` refresh
// handler walks. Membership is mutated only at startup (right
// after each Poller is constructed) and read on every refresh —
// a sync.RWMutex would be over-engineering for a list that
// stops growing the moment the program enters its event loop, so
// a plain Mutex is enough; the cost is bounded by O(pollers).
type pollerRegistry struct {
	mu      sync.Mutex
	pollers []*poll.Poller
}

// Add registers a Poller. Called from startBackendPoller during
// startup; the goroutine is still safe to grow the slice because
// the App's Refresh handler only fires after the user can type,
// which happens after Run starts and Add has settled.
func (r *pollerRegistry) Add(p *poll.Poller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pollers = append(r.pollers, p)
}

// Refresh nudges every poller matching (resource, scope) to fetch
// now. Scope follows the same shape the silences / alerts pages
// use: "all" / "" / single-tenant / comma-joined subset. An
// unrecognised resource quietly no-ops — the page emits "alerts"
// / "silences" / "receivers" / "groups", and a typo is recoverable
// without crashing the loop.
func (r *pollerRegistry) Refresh(resource, scope string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pollers {
		if p.Resource() != resource {
			continue
		}
		if !scopeMatches(scope, p.Tenant()) {
			continue
		}
		p.Refresh()
	}
}

// scopeMatches mirrors the pages' scopeIncludes: empty or "all"
// covers every tenant; comma-joined lists exact-match per element.
// Defined here, not on the pages, because the wiring layer is the
// only consumer that reasons about a scope without owning a page.
// `tenantName` rather than `tenant` to keep the local symbol from
// shadowing the imported `tenant` package.
func scopeMatches(scope, tenantName string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == "all" {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == tenantName {
			return true
		}
	}
	return false
}
