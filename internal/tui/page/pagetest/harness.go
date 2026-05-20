// SPDX-License-Identifier: Apache-2.0

// Package pagetest is the test-only harness shared across page-test
// suites. It absorbs the Update / View+Strip lifecycle scaffolding
// every page test used to copy line-for-line, plus the fixture
// builders for the three domain values (Alert, Silence, AlertGroup)
// that the per-page mk-helpers used to duplicate.
//
// pagetest deliberately does NOT import any concrete page package
// (silences, alerts, ...) — page packages own their own option
// shapes and message types, and the harness operates on the
// app.Page interface so callers stay in control of construction.
// Each migrated test file still calls `silences.New(silences.Options{
// Styles: pagetest.Styles(t), ...})` directly and hands the result
// to pagetest.New; the harness owns only what's genuinely shared
// (Update sequencing, View+Strip, fixture builders, style cache).
package pagetest

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// Harness wraps an app.Page with the Update / View+Strip
// lifecycle every page test exercises. The wrapped page is
// re-assignable because app.Page.Update returns a possibly-
// derivative Page; the harness threads the replacement through so
// callers don't have to track it themselves.
type Harness struct {
	tb   testing.TB
	page app.Page
}

// New returns a Harness tracking page. The caller constructs the
// page (e.g. silences.New(silences.Options{Styles: pagetest.Styles(t),
// ...})) so the harness stays page-package-agnostic.
func New(tb testing.TB, page app.Page) *Harness {
	tb.Helper()
	if page == nil {
		tb.Fatalf("pagetest.New: page must not be nil")
	}
	return &Harness{tb: tb, page: page}
}

// Update delivers msg to the tracked page, re-assigns the tracked
// page from the returned app.Page, and surfaces the page's Cmd so
// the caller can assert on it. Tests that don't care about the Cmd
// can use Send instead.
func (h *Harness) Update(msg tea.Msg) tea.Cmd {
	next, cmd := h.page.Update(msg)
	if next != nil {
		h.page = next
	}
	return cmd
}

// Send is the no-Cmd-needed sibling of Update — useful in setup
// sequences (priming a DataMsg, walking the cursor with `j`
// keypresses) where the test would otherwise write `_ = h.Update(...)`
// on every line.
func (h *Harness) Send(msg tea.Msg) {
	h.Update(msg)
}

// View renders the tracked page to width × height and returns the
// ANSI-stripped output. Strip-on-the-way-out is non-optional: the
// page-test idiom is plain substring matching on the rendered text,
// and re-importing testutil to strip on every assertion was the
// noise this harness exists to remove.
func (h *Harness) View(width, height int) string {
	return testutil.StripStyle(h.page.View(width, height))
}

// Page returns the currently-tracked app.Page. Tests that need to
// reach into concrete-type internals (e.g. p.view[0].ID on the
// silences page) cast the returned interface back to the concrete
// type — the harness exists to share the lifecycle scaffolding,
// not to hide page-specific state.
func (h *Harness) Page() app.Page {
	return h.page
}
