// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
)

// buildClients constructs one backend.Client per configured backend
// keyed by tenant name. A backend whose factory build fails logs a
// warning and is skipped — the rest still get a client. The
// resulting map is shared between the poller fan-out (read paths)
// and the page factories (write paths) so the two stay in sync.
//
// The User-Agent is identical for every backend per RFC 9110 §10.1.5
// — backends differentiate via the existing tenant header, so a
// per-backend UA would only add noise to backend access logs.
func buildClients(cfg *config.Config, ua string, debugLog *slog.Logger, build clientBuilder, errOut io.Writer) map[string]backend.Client {
	out := make(map[string]backend.Client, len(cfg.Backends))
	var opts []factory.Option
	if debugLog != nil {
		opts = append(opts, factory.WithDebugLog(debugLog))
	}
	for _, be := range cfg.Backends {
		c, err := build(be, ua, opts...)
		if err != nil {
			fmt.Fprintf(errOut, "backend %q: build failed: %v\n", be.Name, err)
			continue
		}
		out[be.Name] = c
	}
	return out
}

// clientBuilder mirrors factory.Build's signature so Deps.BuildClient
// can be assigned to it. Kept as a private alias so buildClients's
// argument list reads with intent rather than the factory-shaped
// signature spelled inline.
type clientBuilder func(cfg config.Backend, ua string, opts ...factory.Option) (backend.Client, error)

// UserAgent returns the RFC 9110 User-Agent string identifying
// this build of a10r. Format: `a10r/<ver>` for plain releases,
// `a10r/<ver> (<comm>)` when a non-default commit is available —
// gives backend operators one grep-able token while keeping the
// header short for log aggregators. The build vars are injected
// at link time by goreleaser and default to "dev"/"none" for
// local builds; tests pass them explicitly so the function does
// not read package state and remains data-race free under
// t.Parallel.
//
// Exported so the cmd package's non-TUI alerts / silences / etc.
// subcommands tag their HTTP traffic with the same UA.
func UserAgent(ver, comm string) string {
	if comm == "" || comm == buildCommitNone {
		return "a10r/" + ver
	}
	return "a10r/" + ver + " (" + comm + ")"
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

// fetchTenantVersions issues one /api/v2/status call per
// configured backend and returns the resolved Alertmanager
// version keyed by backend name. Concurrent fan-out so a slow
// backend doesn't block startup; per-backend timeout caps each
// call so a hung backend doesn't stall the program. Failures
// silently produce an empty entry — the tenant page renders "—"
// for missing versions.
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
