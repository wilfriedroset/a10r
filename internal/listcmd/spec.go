// SPDX-License-Identifier: Apache-2.0

// Package listcmd is the shared orchestrator for the four headless list
// commands (alerts/silences/groups/receivers). It owns the cross-cutting
// concerns: per-backend fan-out via errgroup, lenient partial-failure
// (ADR 0009), TTY-vs-pipe format resolution, pager lifecycle, deterministic
// ordering, and the sentinel errors cmd/ maps to exit codes. Generic over
// the row type R so each command keeps its own projection.
package listcmd

import (
	"context"
	"io"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/output"
)

// Renderer writes the format-specific projection of rows to w.
type Renderer[R any] func(w io.Writer, rows []R) error

// Fetcher fetches the per-backend rows for a single client. Filter logic
// lives in the closure, not the pipeline, so the orchestrator never sees an
// unfiltered slice; (nil, nil) is valid — an empty backend is not a failure.
type Fetcher[R any] func(ctx context.Context, name string, client backend.Client) ([]R, error)

// PagerFactory matches cmd.NewPager's signature so the production wrapper is a
// forwarding one-liner; tests inject a write-through fake to avoid spawning less.
type PagerFactory func(ctx context.Context, fallback io.Writer, outIsTerminal, noPager bool) (io.WriteCloser, error)

// ClientFactory builds one backend.Client per configured backend. Mirrors
// boot.Deps.BuildClient so the two orchestrators share an injection idiom.
type ClientFactory func(cfg config.Backend) (backend.Client, error)

// Deps holds the injectable construction seams. Zero value is valid: every
// nil field falls back to its production default (see resolved).
type Deps struct {
	BuildClient ClientFactory

	PagerFactory PagerFactory

	// Stderr receives per-backend fetch errors, sorted by backend name for
	// deterministic output. Nil discards the lines.
	Stderr io.Writer
}

// Spec is the per-run input cmd/ hands to Run. Fields are ordered as the
// pipeline consumes them.
type Spec[R any] struct {
	Config *config.Config

	// Format is the pre-parsed --output value; empty resolves per TTY-vs-pipe.
	Format output.Format

	Fetcher Fetcher[R]

	// Renderers maps each supported format to its writer; an unmatched format
	// fails closed with a precise error rather than printing nothing.
	Renderers map[output.Format]Renderer[R]

	// Sort mutates the row slice in place.
	Sort func([]R)

	// ResourceLabel is the singular noun ErrMatched templates into
	// "--fail: N <label>(s) matched the filter".
	ResourceLabel string

	FailOnAny bool

	NoPager bool

	Out io.Writer

	Deps Deps
}

// resolved returns a copy of d with every nil seam replaced by its production
// default, keeping Run's body free of nil-check noise.
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
