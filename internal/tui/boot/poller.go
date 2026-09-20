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

const (
	resourceAlerts    = "alerts"
	resourceSilences  = "silences"
	resourceReceivers = "receivers"
	resourceStatus    = "status"
)

// startBackendPoller spawns the per-(backend, resource) poller
// matrix. Each entry in clients gets one poller per resource
// (alerts, silences, receivers, status), and every emitted DataMsg
// carries the backend's tenant tag so list pages can union
// snapshots into a `byTenant` map and reason about scope at render
// time.
//
// The four resources share a single interval per backend: poll
// pressure is dominated by the alerts feed, and the others are
// cheap reads that piggy-back. Configurable per-resource intervals
// are deferred — overkill at the current fan-out.
//
// reg takes ownership of the resulting set, so the App's `r` refresh
// handler can find an entry by (resource, tenant) and a `:reload`
// can rebuild the whole matrix under a new interval.
func startBackendPoller(ctx context.Context, cfg *config.Config, clients map[string]backend.Client, send func(tea.Msg), reg *pollerRegistry) func() {
	if len(clients) == 0 {
		return func() {}
	}
	reg.setSpawn(func(c *config.Config) []*poll.Poller {
		return spawnPollers(ctx, c, clients, send)
	})
	reg.Restart(cfg)
	return reg.stopAll
}

func spawnPollers(ctx context.Context, cfg *config.Config, clients map[string]backend.Client, send func(tea.Msg)) []*poll.Poller {
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
		}
	}
	return pollers
}

// backendInterval picks the active poll interval for a backend
// without considering page-level overrides. Per-backend
// `poll_interval` wins; falls back to the global default;
// ultimate fallback is 1 minute (config.DefaultPollInterval).
func backendInterval(be config.Backend, cfg *config.Config) time.Duration {
	if be.PollInterval > 0 {
		return be.PollInterval
	}
	if cfg.Defaults.PollInterval > 0 {
		return cfg.Defaults.PollInterval
	}
	return time.Minute
}

// pollResources is every label backendFetchers emits, which is also
// every key pageOverride answers for. Named so a reload can ask
// about each tick a backend runs without building the pollers.
var pollResources = []string{resourceAlerts, resourceSilences, resourceReceivers, resourceStatus}

// pageInterval layers the per-page override (cfg.Pages.<page>) on
// top of backendInterval. The resource argument matches the
// labels backendFetchers emits ("alerts", "silences",
// "receivers", "status") and the per-page YAML field names. A
// non-zero override wins over both the per-backend value
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
	case resourceAlerts:
		return p.Alerts.PollInterval
	case resourceSilences:
		return p.Silences.PollInterval
	case resourceReceivers:
		return p.Receivers.PollInterval
	case resourceStatus:
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

// backendFetchers returns the four poller fetch funcs for one
// backend client — alerts, silences, receivers, status. Each
// returns the resource as `any` so poll.Options.Fetch can be a
// single shape across resource types. The resource labels must
// match the strings the pages declare via PollResources() and
// emit on RefreshRequestedMsg ("alerts", "silences", "receivers",
// "status").
//
// `status` joins the matrix so the status page's version / uptime
// / config refresh on the configured interval instead of freezing
// on the cold-start snapshot.
func backendFetchers(c backend.Client) []fetcherEntry {
	return []fetcherEntry{
		{resource: resourceAlerts, fetch: func(ctx context.Context) (any, error) {
			return c.ListAlerts(ctx, backend.AlertFilter{})
		}},
		{resource: resourceSilences, fetch: func(ctx context.Context) (any, error) {
			return c.ListSilences(ctx, backend.SilenceFilter{})
		}},
		{resource: resourceReceivers, fetch: func(ctx context.Context) (any, error) {
			return c.ListReceivers(ctx)
		}},
		{resource: resourceStatus, fetch: func(ctx context.Context) (any, error) {
			return c.Status(ctx)
		}},
	}
}

// pollerRegistry is the wiring-layer index the App's `r` refresh
// handler walks. Membership is mutated at startup (right after each
// Poller is constructed) and again on a `:reload` that changed a poll
// interval, and read on every refresh — a sync.RWMutex would be
// over-engineering for a list that changes twice in a session, so a
// plain Mutex is enough; the cost is bounded by O(pollers).
type pollerRegistry struct {
	mu      sync.Mutex
	pollers []*poll.Poller
	// spawn builds a fresh poller set under a config. Installed by
	// startBackendPoller, which is the only place holding the ctx,
	// the clients and the send func a Poller needs. Nil until then,
	// which is what makes a Restart before the start a no-op.
	spawn func(*config.Config) []*poll.Poller
}

// Restart rebuilds every poller under the new config, because
// poll.Poller reads its interval once at construction and a session
// that kept the old one would contradict the config the user is now
// reading. A registry whose pollers have not started yet has nothing
// to rebuild: Build returns before cmd/tui.go starts them.
//
// The outgoing set is stopped off this goroutine on purpose. A
// reload runs inside App.Update, which is the goroutine draining
// bubbletea's unbuffered message channel, and Poller.Stop joins a
// goroutine that can be parked in Send waiting for exactly that
// drain. Joining here wedges the TUI past recovery, because Ctrl+C
// travels the same channel. Detached, Update returns, the parked
// sends land, and the outgoing pollers wind down.
//
// The cost is a snapshot from the outgoing set landing after one
// from the new set. The App's cache is last-write-wins per
// (resource, tenant), so the next tick corrects it.
func (r *pollerRegistry) Restart(cfg *config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.spawn == nil {
		return
	}
	outgoing := r.pollers
	r.pollers = r.spawn(cfg)
	go stopPollers(outgoing)
}

// stopAll stops every live poller and waits for them, unlike
// Restart: this runs from cmd/tui.go after program.Run returned, so
// the program context is done and a parked Send has already
// unblocked. Waiting is what keeps a10r from exiting with the live
// set still in flight. A set an earlier Restart detached is not
// covered: it is already cancelled and winds down on its own.
//
// Safe to call more than once: the set is dropped under the lock, so
// a second call has nothing to stop.
func (r *pollerRegistry) stopAll() {
	r.mu.Lock()
	outgoing := r.pollers
	r.pollers = nil
	r.mu.Unlock()
	stopPollers(outgoing)
}

// stopPollers winds down a set the registry no longer indexes, so
// Refresh cannot nudge a poller that is already stopping.
func stopPollers(pollers []*poll.Poller) {
	for _, p := range pollers {
		p.Stop()
	}
}

func (r *pollerRegistry) setSpawn(f func(*config.Config) []*poll.Poller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spawn = f
}

func (r *pollerRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pollers)
}

// Refresh nudges every poller matching (resource, scope) to fetch
// now. Scope follows the same shape the silences / alerts pages
// use: "all" / "" / single-tenant / comma-joined subset. An
// unrecognised resource quietly no-ops — the page emits "alerts"
// / "silences" / "receivers", and a typo is recoverable without
// crashing the loop.
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
