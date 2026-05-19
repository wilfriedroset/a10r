// SPDX-License-Identifier: Apache-2.0

// Package listcmd is the shared orchestrator the four headless list
// commands (alerts/silences/groups/receivers list) run on top of.
// Each command stays in cmd/ but shrinks to: parse flags, validate
// per-command options, build a Spec[R] (with its fetcher and renderer
// map), then call Run. The pipeline owns the cross-cutting concerns:
// per-backend fan-out (parallel via errgroup), lenient partial-failure
// (ADR 0009), TTY-vs-pipe format resolution, pager lifecycle,
// deterministic output ordering, and the canonical errors the cmd
// layer translates to exit codes.
//
// The package is generic over the row type so each command keeps its
// own JSON/YAML/table projection without sharing a row schema — the
// only contract the pipeline cares about is "the fetcher returns
// []R, the renderer map writes []R to an io.Writer".
package listcmd

import (
	"context"
	"io"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/output"
)

// Renderer writes the format-specific projection of rows to w. The
// pipeline switches on the resolved format and calls exactly one
// renderer per run; each command supplies a map keyed by every
// supported output.Format so an unhandled format trips a precise
// error rather than a silent default.
type Renderer[R any] func(w io.Writer, rows []R) error

// Fetcher fetches the per-backend rows for a single client and tags
// them with name (the backend's config name). Filter logic lives
// inside the fetcher closure, not in the pipeline: the orchestrator
// never sees an unfiltered slice. Returning (nil, nil) is valid —
// a backend with no rows is not a failure.
type Fetcher[R any] func(ctx context.Context, name string, client backend.Client) ([]R, error)

// PagerFactory matches cmd.NewPager's signature byte-for-byte so the
// production wrapper is a one-liner forwarding call. The factory is
// injected through Deps so tests can substitute a write-through
// fake and never spawn a less subprocess.
type PagerFactory func(ctx context.Context, fallback io.Writer, outIsTerminal, noPager bool) (io.WriteCloser, error)

// ClientFactory builds one backend.Client per configured backend.
// Production wires factory.Build; tests inject a fake that returns
// a stub client and lets the test assert which clients the pipeline
// requested. Mirrors the boot.Deps.BuildClient seam so the two
// orchestrators share an injection idiom.
type ClientFactory func(cfg config.Backend) (backend.Client, error)

// Deps holds the construction-time seams the pipeline lets callers
// override. Zero value is valid: every nil field falls back to its
// production default (see resolved). Mirrors internal/tui/boot.Deps
// so the two orchestrators share an injection idiom — tests build
// a Deps{} with only the fields they need and let the rest default.
type Deps struct {
	// BuildClient constructs a backend.Client for one config.Backend.
	// Production default is supplied by the cmd layer (factory.Build
	// wrapped to inject the User-Agent + factory options computed
	// from --debug-http). Pipeline-level tests inject a fake that
	// returns a stub Client without touching net/http.
	BuildClient ClientFactory

	// PagerFactory opens the pager subprocess. Production default
	// wraps cmd.NewPager so an interactive table run lands in less;
	// non-TTY / --no-pager / non-table runs get a write-through
	// pager with a no-op Close. Tests inject a write-through fake.
	PagerFactory PagerFactory

	// Stderr is the destination for per-backend fetch errors. The
	// pipeline sorts errors by backend name then prints one per
	// line, mirroring today's `fmt.Fprintln(os.Stderr, e)` loop.
	// Nil falls back to discarding the lines — the production
	// caller wires os.Stderr.
	Stderr io.Writer
}

// Spec is the per-run input the cmd layer hands to Run. The fields
// document a contract; the pipeline body sequences them. Documented
// in field order (the order the pipeline consumes them) so the file
// reads top-to-bottom as a flow diagram.
type Spec[R any] struct {
	// Config carries the loaded a10r.yaml. The pipeline does not
	// load config itself — that is cmd/'s job because cmd/ owns the
	// ExitConfigInvalid mapping and the validate.go reporting path.
	Config *config.Config

	// Format is the user's pre-parsed --output value. Empty means
	// "default per TTY-vs-pipe" and the pipeline resolves via
	// output.Resolve at the same point cmd/ used to.
	Format output.Format

	// Fetcher fans the per-backend list call out into []R. Filter
	// logic is the fetcher's responsibility: the pipeline never
	// sees an unfiltered slice, so per-command predicates do not
	// leak into the shared orchestrator.
	Fetcher Fetcher[R]

	// Renderers maps each supported output.Format onto the writer
	// that emits the format-specific projection. The pipeline
	// switches on the resolved format and returns a precise error
	// when no renderer matches — the empty-map case fails closed
	// rather than printing nothing.
	Renderers map[output.Format]Renderer[R]

	// Sort mutates the accumulated row slice in place. Mirrors the
	// existing sortAlertRows / sortSilenceRows / sortGroupRows /
	// sortReceiverRows shape so per-command sort logic does not
	// need a return-shape refactor to plug into the pipeline.
	Sort func([]R)

	// ResourceLabel is the singular noun the ErrMatched message
	// uses ("alert" / "silence" / "group" / "receiver"). Pipeline
	// templates "--fail: N <label>(s) matched the filter" so the
	// rendered error matches today's per-command string.
	ResourceLabel string

	// FailOnAny mirrors the per-command --fail flag. When true and
	// the final row slice is non-empty, Run wraps ErrMatched with
	// the count + label so cmd/ can map onto ExitFailMatched.
	FailOnAny bool

	// NoPager mirrors the global --no-pager flag. Forwarded to
	// PagerFactory verbatim; the pipeline does not interpret it.
	NoPager bool

	// Out is the post-pager sink — typically os.Stdout. The
	// pipeline wraps it in the pager subprocess when conditions
	// are met, otherwise it writes through.
	Out io.Writer

	// Deps holds the injectable construction seams. Zero value is
	// valid; resolved() fills in production defaults.
	Deps Deps
}

// resolved returns a copy of d with every nil seam replaced by its
// production default. Concentrating the defaulting in one method
// keeps Run's body free of nil-check noise — the stages read with
// intent. Mirrors internal/tui/boot.Deps.resolved.
func (d Deps) resolved() Deps {
	out := d
	if out.PagerFactory == nil {
		out.PagerFactory = defaultPagerFactory
	}
	if out.Stderr == nil {
		out.Stderr = io.Discard
	}
	return out
}
