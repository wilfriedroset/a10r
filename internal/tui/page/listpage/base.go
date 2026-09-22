// SPDX-License-Identifier: Apache-2.0

// Package listpage holds the shared base for the list-style pages
// (alerts, silences, receivers). Helpers earn their place at
// 3+ callers. Base does NOT implement tea.Model — pages embed it and
// call in explicitly. See ADR 0013. Cursor state lives in the
// embedded cursor.Window so the reconcile-on-change invariant is a
// type property, not a convention — see ADR 0016.
package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"

	tea "charm.land/bubbletea/v2"
)

// Base holds the type-independent fields every list page needs.
// Recompute is the per-page rebuild callback wired by each
// constructor.
type Base struct {
	cursor.Window
	// filter is the active buffer and its classification, held
	// together and written only by SetFilter.
	filter filterexpr.Compiled
	// PreFilter is the pre-prompt snapshot restored on filter
	// cancel. Nil iff no filter prompt is open — relies on the App
	// auto-forwarding PromptOpenedMsg to the top page.
	PreFilter *string
	// FilterErr is non-nil while the open prompt buffer cannot be
	// applied. The rows stay on the last good filter and the chrome
	// renders the reason in place of the mode tag.
	FilterErr error
	// Grammar declares which term languages this page's `/` buffer
	// reads. The zero value is text only; the alerts list and group
	// detail declare AlertGrammar because they also accept label
	// selectors and boolean expressions. Set it once at
	// construction, before the first SetFilter: a later change
	// leaves the stored classification behind.
	Grammar filterexpr.Grammar
	Scope   string
	// Paused suppresses the recompute branch on poll.DataMsg so the
	// table stops updating under the cursor mid-read. Toggled by `w`.
	Paused bool
	// BackendHealth holds per-tenant transport state for the error
	// band; an entry exists only while the tenant is not connected.
	// See ADR-0014.
	BackendHealth map[string]BackendHealth
	// Tenants is the canonical configured-backend list. Drives
	// TENANT-column visibility so a tenant that never replies still
	// counts toward "is this a multi-tenant fleet?".
	Tenants   []string
	Recompute func()
	// RowCount returns the rows the page currently presents. Panics
	// on nil when GoToFirstRowMsg arrives — see ADR-0018.
	RowCount func() int
	// SnapshotFocus captures the current row's identity so the next
	// recompute re-resolves it. Panics on nil when GoToFirstRowMsg
	// arrives — see ADR-0018.
	SnapshotFocus func()
	// SetTimeFormat applies a TimeFormatChangedMsg. Nil on pages
	// that render no time (receivers); nil is treated as a
	// fall-through by HandleSidebandMsg — see ADR-0018.
	SetTimeFormat func(timerender.Format)
	// SetStateFormat applies a StateFormatChangedMsg. Nil except on
	// the alerts list and group detail; nil falls through — see
	// ADR-0018.
	SetStateFormat func(stateformat.Format)
	// SetReadOnly applies a ReadOnlyChangedMsg. Nil on pages with no
	// Dangerous verb (receivers); nil falls through — see ADR-0018.
	SetReadOnly func(bool)
	// ClearMarks runs the page's mark-clearing routine and returns
	// any follow-up flash command. Nil on pages without marks; nil
	// falls through — see ADR-0018.
	ClearMarks func() tea.Cmd
	// Visual is the range-mark anchor `V` drops. Lives on Base so the
	// Esc contract has one implementation; a page without marks never
	// starts it, so the zero value keeps it inert.
	Visual Visual
}

// Suspend drops the open visual range when another page is pushed
// on top. Visual mode is page-local by contract, so a drill-down
// ends it rather than leaving a preview to reappear on the way back.
// Implements app.Suspender.
func (b *Base) Suspend() { b.Visual.Cancel() }

// ConsumeEscape cancels an open visual range and reports that it took
// the key, so Esc unwinds the range before the global binding pops
// the page. Implements app.EscapeConsumer.
func (b *Base) ConsumeEscape() bool {
	if !b.Visual.On() {
		return false
	}
	b.Visual.Cancel()
	return true
}

// ValidateFilter reports why s cannot be applied as this page's
// filter, or nil when it can. Doubles as the prompt's Enter-time gate.
func (b *Base) ValidateFilter(s string) error {
	_, err := filterexpr.Compile(s, b.Grammar)
	//nolint:wrapcheck // Compile names the grammar that refused the buffer, which is what the prompt renders
	return err
}

// FilterError exposes the unusable-buffer reason to the app chrome,
// which cannot read the field directly: listpage imports app, not the
// reverse, so the seam has to be a method.
func (b *Base) FilterError() error { return b.FilterErr }
