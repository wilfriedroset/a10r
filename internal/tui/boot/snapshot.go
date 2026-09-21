// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// Snapshot defaults, exported so the cmd layer binds its flags to
// the same numbers the renderer falls back to.
const (
	DefaultSnapshotWidth  = 120
	DefaultSnapshotHeight = 40

	// defaultSnapshotWait caps how long a snapshot waits for the
	// first poll. Long enough for a healthy backend on a slow link,
	// short enough that CI does not stall on a dead one.
	defaultSnapshotWait = 3 * time.Second
)

// ErrUnknownPage is returned when SnapshotOptions.Page is not one
// of SnapshotPages.
var ErrUnknownPage = errors.New("unknown page")

// SnapshotPages lists the pages Snapshot can render, in the order
// the error message offers them.
func SnapshotPages() []string {
	return []string{resourceAlerts, resourceSilences, resourceStatus, resourceReceivers, pageTenant}
}

// SnapshotOptions configures one headless frame render.
type SnapshotOptions struct {
	// Page is the `:` alias of the page to render, from SnapshotPages.
	Page string
	// Width and Height are the terminal cell dimensions to lay the
	// frame out for. Non-positive values fall back to the defaults.
	Width, Height int
	// Color keeps the skin's SGR escapes in the returned frame.
	// Off (the default) strips them, which is what a plain-text
	// golden or a piped diff wants.
	Color bool
	// Wait caps how long the render waits for every backend's first
	// poll. Zero picks the built-in cap of 3 s.
	Wait time.Duration
}

// Snapshot renders one settled frame of the requested page and
// returns it. It drives the same bubbletea program the TUI runs,
// with the renderer and the input disabled, so the frame is the
// assembled article — top panel, body, footer — rather than a
// page's View in isolation.
//
// The render waits for the first poll of every backend (see
// pollGate) up to opts.Wait, then quits the program and reads the
// final model's View. Reading after Run returns keeps the frame
// race-free: quitWithCleanup closes the pages but leaves the stack
// standing, so the settled view is still there to render.
//
// Callers own the Result lifecycle exactly as cmd/tui.go does:
// Build, defer Close, then Snapshot.
func (r *Result) Snapshot(ctx context.Context, opts SnapshotOptions) (string, error) {
	push, err := r.resolveSnapshotPage(opts.Page)
	if err != nil {
		return "", err
	}

	width, height := sizeOr(opts.Width, DefaultSnapshotWidth), sizeOr(opts.Height, DefaultSnapshotHeight)
	prog := r.snapshotProgram(ctx, width, height)

	done := make(chan struct{})
	var (
		final  tea.Model
		runErr error
	)
	go func() {
		defer close(done)
		final, runErr = prog.Run()
	}()

	// Send blocks until the event loop is reading, and the loop
	// consumes its queue in order, so everything sent here — before
	// the pollers exist — reaches the App before the first DataMsg
	// and before the Quit below.
	//
	// The size is sent by hand even though WithWindowSize is set:
	// bubbletea delivers its own initial WindowSizeMsg from a
	// goroutine, so a snapshot whose backends settle fast can quit
	// before that message ever lands and render an unsized frame.
	// WithWindowSize still matters — it gives that stray message the
	// same numbers, so a late delivery cannot resize the frame.
	prog.Send(tea.WindowSizeMsg{Width: width, Height: height})
	for _, name := range r.clientlessBackends() {
		prog.Send(poll.BackendStatusMsg{
			Tenant: name, State: header.ConnUnreachable, Detail: "client build failed",
		})
	}
	prog.Send(push())

	gate := newPollGate(r.cfg, r.clients)
	stop := r.StartPollers(ctx, func(msg tea.Msg) {
		// Send before observe: Send returns only once the loop has
		// taken the message, so its Update completes before the
		// QuitMsg the opened gate triggers.
		prog.Send(msg)
		gate.observe(msg)
	})
	defer stop()

	wait := waitOr(opts.Wait)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	select {
	case <-gate.ready:
	case <-deadline.C:
		// Stderr, not slog: the default logger writes to the log
		// file, and spec item 9 wants the "this frame is partial"
		// caveat where the operator running the command will see
		// it. stdout stays frame-only per ADR 0045.
		fmt.Fprintf(r.stderr, "warning: %s frame rendered after %s without a report from %s\n",
			opts.Page, wait, strings.Join(gate.pending(), ", "))
	case <-done:
	}

	prog.Quit()
	<-done
	if runErr != nil {
		return "", fmt.Errorf("render frame: %w", runErr)
	}
	a, ok := final.(*app.App)
	if !ok {
		return "", fmt.Errorf("render frame: bubbletea returned a %T, not the App model", final)
	}
	frame := a.View().Content
	if !opts.Color {
		frame = ansi.Strip(frame)
	}
	return frame, nil
}

// snapshotProgram builds the headless bubbletea program a snapshot
// drives: no renderer, no input, no output, and a window size that
// comes from the flags rather than from a terminal.
//
// tea.WithContext is set here although cmd/tui.go deliberately omits
// it: there a cancelled ctx would skip the page-stack Close cascade
// on SIGTERM. A snapshot wants exactly that abort, because a
// cancelled render has no frame to hand back and the process exits
// straight after. Close still runs from the caller's defer.
func (r *Result) snapshotProgram(ctx context.Context, width, height int) *tea.Program {
	return tea.NewProgram(r.app,
		tea.WithContext(ctx),
		tea.WithFilter(QuitFilter(r.app)),
		tea.WithoutRenderer(),
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithWindowSize(width, height),
	)
}

// resolveSnapshotPage turns a page name into the command that
// pushes it. The name is matched exactly against SnapshotPages
// first: the cmdbar resolver also answers prefixes and `:q`, and a
// snapshot of the quit command is not a frame anyone asked for.
func (r *Result) resolveSnapshotPage(name string) (tea.Cmd, error) {
	if !slices.Contains(SnapshotPages(), name) {
		return nil, fmt.Errorf("%w: %q — want one of %s", ErrUnknownPage, name, strings.Join(SnapshotPages(), ", "))
	}
	cmd, err := r.resolver.Resolve(name)
	if err != nil {
		return nil, fmt.Errorf("resolve page %q: %w", name, err)
	}
	return cmd, nil
}

// clientlessBackends names the configured backends that got no
// client. The names come from the two maps rather than from a field
// Build fills, because a derived answer cannot drift from the map
// the pollers actually run over.
func (r *Result) clientlessBackends() []string {
	var out []string
	for _, be := range r.cfg.Backends {
		if _, ok := r.clients[be.Name]; !ok {
			out = append(out, be.Name)
		}
	}
	return out
}

func sizeOr(got, fallback int) int {
	if got <= 0 {
		return fallback
	}
	return got
}

func waitOr(got time.Duration) time.Duration {
	if got <= 0 {
		return defaultSnapshotWait
	}
	return got
}

// pollGate closes ready once every poller startBackendPoller
// spawned has reported once, so a snapshot renders a filled frame
// instead of the cold-start one. Without it the render would have
// to burn the whole wait on every run.
type pollGate struct {
	mu sync.Mutex
	// left maps a tenant to the resources it still owes a report
	// for. A tenant drops out of the map once its set empties.
	left  map[string]map[string]struct{}
	ready chan struct{}
}

// newPollGate builds the gate over the same (backend, resource)
// matrix startBackendPoller spawns, so the two cannot drift: a
// backend whose client failed to build has no poller and is not
// waited for.
func newPollGate(cfg *config.Config, clients map[string]backend.Client) *pollGate {
	g := &pollGate{left: make(map[string]map[string]struct{}), ready: make(chan struct{})}
	for _, be := range cfg.Backends {
		c, ok := clients[be.Name]
		if !ok {
			continue
		}
		owed := make(map[string]struct{})
		for _, entry := range backendFetchers(c) {
			owed[entry.resource] = struct{}{}
		}
		g.left[be.Name] = owed
	}
	if len(g.left) == 0 {
		close(g.ready)
	}
	return g
}

// observe records one poller report. A DataMsg settles its own
// (tenant, resource) pair. An unreachable BackendStatusMsg settles
// the whole tenant instead: the message carries no resource label,
// and a backend nothing can reach will only repeat the failure
// until the deadline, so the degraded band it produces is already
// the frame the user sees. ConnDegraded is deliberately not a
// shortcut — poll.stateFromErr raises it for a single failing
// endpoint, and the other resources on that tenant are still worth
// waiting for.
func (g *pollGate) observe(msg tea.Msg) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.left) == 0 {
		return
	}
	switch m := msg.(type) {
	case poll.DataMsg:
		delete(g.left[m.Tenant], m.ResourceLabel)
		if len(g.left[m.Tenant]) == 0 {
			delete(g.left, m.Tenant)
		}
	case poll.BackendStatusMsg:
		if m.State != header.ConnUnreachable {
			return
		}
		delete(g.left, m.Tenant)
	default:
		return
	}
	if len(g.left) == 0 {
		close(g.ready)
	}
}

// pending returns the (tenant, resource) pairs that never reported,
// for the deadline warning.
func (g *pollGate) pending() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for tenant, owed := range g.left {
		for resource := range owed {
			out = append(out, tenant+"/"+resource)
		}
	}
	slices.Sort(out)
	return out
}
