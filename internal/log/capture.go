// SPDX-License-Identifier: Apache-2.0

package log

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Capture buffers the warnings emitted inside an explicitly opened
// window so a reader other than the log file can show them. Startup
// warnings — an unknown skin, a shadowed skin, a backend whose client
// failed to build — otherwise only reach the file, which is the one
// place an operator cannot look at without leaving the TUI.
//
// The zero value is ready and captures nothing until Start. Every
// record still reaches the wrapped handler either way: this is a
// side-channel, never a filter.
type Capture struct {
	mu       sync.Mutex
	open     bool
	messages []string
}

// Start opens a window and drops whatever an earlier one collected.
// A reader wants the warnings of one run, not every run since the
// process began, and an accumulating buffer would also grow without
// bound.
func (c *Capture) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.open = true
	c.messages = nil
}

// Stop closes the window. The collected messages stay readable.
func (c *Capture) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.open = false
}

// Messages returns a copy of what the last window collected, oldest
// first.
func (c *Capture) Messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return nil
	}
	return append([]string(nil), c.messages...)
}

// Wrap returns a handler that records warnings into c on the way to
// inner. Call it once, around the process handler, at logger
// construction.
func (c *Capture) Wrap(inner slog.Handler) slog.Handler {
	return &capturingHandler{capture: c, inner: inner}
}

func (c *Capture) isOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open
}

func (c *Capture) record(r slog.Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open {
		return
	}
	c.messages = append(c.messages, formatRecord(r))
}

// formatRecord folds a record into the one line the page prints. The
// attributes join the message because the value a warning names is the
// part that tells the operator what to fix.
func formatRecord(r slog.Record) string {
	attrs := make([]string, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
		return true
	})
	if len(attrs) == 0 {
		return r.Message
	}
	return r.Message + " (" + strings.Join(attrs, ", ") + ")"
}

type capturingHandler struct {
	capture *Capture
	inner   slog.Handler
}

// Enabled admits warnings while the window is open even when the
// sink itself would drop them, because `--quiet` raises the file's
// level and the page must still answer "what went wrong at start".
// Handle re-checks the sink, so the file keeps its own level.
func (h *capturingHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	if lvl >= slog.LevelWarn && h.capture.isOpen() {
		return true
	}
	return h.inner.Enabled(ctx, lvl)
}

func (h *capturingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		h.capture.record(r)
	}
	if !h.inner.Enabled(ctx, r.Level) {
		return nil
	}
	if err := h.inner.Handle(ctx, r); err != nil {
		return fmt.Errorf("handle log record: %w", err)
	}
	return nil
}

func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &capturingHandler{capture: h.capture, inner: h.inner.WithAttrs(attrs)}
}

func (h *capturingHandler) WithGroup(name string) slog.Handler {
	return &capturingHandler{capture: h.capture, inner: h.inner.WithGroup(name)}
}
