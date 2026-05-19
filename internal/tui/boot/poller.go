// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"context"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// startBackendPoller spawns the per-(backend, resource) poller
// matrix per audit §5.1. Each entry in clients gets one poller
// per resource (alerts, silences, receivers, alert-groups,
// status), and every emitted DataMsg carries the backend's tenant
// tag so list pages can union snapshots into a `byTenant` map and
// reason about scope at render time.
//
// The five resources share a single interval per backend: poll
// pressure is dominated by the alerts feed, and the others are
// cheap reads that piggy-back. Configurable per-resource intervals
// are deferred — overkill for v0.1 and not in the audit.
//
// reg is published with each poller so the App's `r` refresh
// handler can find the matching entry by (resource, tenant).
func startBackendPoller(ctx context.Context, cfg *config.Config, clients map[string]backend.Client, send func(tea.Msg), reg *pollerRegistry) func() {
	if len(clients) == 0 {
		return func() {}
	}
	pollers := make([]*poll.Poller, 0, len(clients)*4)
	for _, be := range cfg.Backends {
		c, ok := clients[be.Name]
		if !ok {
			continue // factory.Build failed in buildClients; warning already emitted
		}
		name := be.Name
		for _, entry := range backendFetchers(c) {
			p := poll.New(poll.Options{
				Tenant:   name,
				Resource: entry.resource,
				Interval: pageInterval(be, cfg, entry.resource),
				Fetch:    entry.fetch,
				Send:     send,
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

// backendInterval picks the active poll interval for a backend
// without considering page-level overrides. Per-backend
// `poll_interval` wins; falls back to the global default;
// ultimate fallback is 1 minute (audit §5.1, I3).
func backendInterval(be config.Backend, cfg *config.Config) time.Duration {
	if be.PollInterval > 0 {
		return be.PollInterval
	}
	if cfg.Defaults.PollInterval > 0 {
		return cfg.Defaults.PollInterval
	}
	return time.Minute
}

// pageInterval layers the per-page override (cfg.Pages.<page>) on
// top of backendInterval. The resource argument matches the
// labels backendFetchers emits ("alerts", "silences",
// "receivers", "groups", "status") and the per-page YAML field
// names. A non-zero override wins over both the per-backend value
// and the global default.
//
// Resources that are NOT user-overrideable (an unknown label,
// e.g. a future resource a user hasn't pinned) silently fall
// through to backendInterval — the page-override config is
// strictly additive, never required.
func pageInterval(be config.Backend, cfg *config.Config, resource string) time.Duration {
	if override := pageOverride(cfg.Pages, resource); override > 0 {
		return override
	}
	return backendInterval(be, cfg)
}

// pageOverride extracts the per-page poll-interval override for
// the named resource. Returns 0 when the user has not configured
// the page or the resource is unknown — the caller treats either
// case as "use the resolved default".
func pageOverride(p config.PageOverrides, resource string) time.Duration {
	switch resource {
	case "alerts":
		return p.Alerts.PollInterval
	case "silences":
		return p.Silences.PollInterval
	case "groups":
		return p.Groups.PollInterval
	case "receivers":
		return p.Receivers.PollInterval
	case "status":
		return p.Status.PollInterval
	default:
		return 0
	}
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

// backendFetchers returns the five poller fetch funcs for one
// backend client — alerts, silences, receivers, alert-groups,
// status. Each returns the resource as `any` so poll.Options.Fetch
// can be a single shape across resource types. The resource labels
// must match the strings the pages declare via PollResources() and
// emit on RefreshRequestedMsg ("alerts", "silences", "receivers",
// "groups", "status").
//
// `status` joins the matrix so the status page's version / uptime
// / config refresh on the configured interval instead of freezing
// on the cold-start snapshot.
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
		{resource: "status", fetch: func(ctx context.Context) (any, error) {
			return c.Status(ctx)
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
	if scope == "" || scope == scopeAll {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == tenantName {
			return true
		}
	}
	return false
}
