// SPDX-License-Identifier: Apache-2.0

package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Page is one frame in the page stack. The App owns the stack;
// pages own their own state, dispatch their own messages, and
// describe their own crumbs / header content / bindings so the
// app shell stays page-agnostic.
//
// Update returns the same Page interface (not a concrete type)
// because the stack stores them by interface — a concrete-typed
// return would force every Update call to round-trip through a
// type switch. Either pointer or value receivers work; pages that
// mutate significant internal state in Update typically pick
// pointer receivers, while pure-data pages (status panes etc.)
// can stay value-typed.
type Page interface {
	// Init is called when the page is pushed onto the stack. The
	// returned Cmd runs in the App's Update cycle, so a page can
	// kick off polling, fetch its initial data, etc.
	Init() tea.Cmd

	// Update routes a message into the page. Returns a possibly-
	// derivative Page and an optional Cmd. Messages flow to the
	// top-of-stack page only; bottom pages are paused.
	Update(msg tea.Msg) (Page, tea.Cmd)

	// Close releases the page's resources before it leaves the
	// stack via pop or replace. The returned Cmd typically cancels
	// any background work the page started in Init (poller stops,
	// HTTP cancellations). nil is fine when nothing needs tearing
	// down. The app shell guarantees Close runs exactly once per
	// page instance — pages must not assume a paired Init.
	Close() tea.Cmd

	// View renders the page body to fit width × height. The app
	// shell composes the result with the header above and the
	// footer below.
	View(width, height int) string

	// Crumb is the breadcrumb label rendered in the footer's crumb
	// strip. The bottom page is rendered first; the top page wears
	// the active highlight.
	Crumb() string

	// Title is the page title rendered in the bordered body's top
	// edge — k9s-style "<resource>(<scope>)[<count>]" e.g.
	// "alerts(prod)[531]". Empty falls back to Crumb.
	Title() string

	// HeaderContent is the per-view middle-zone slot string in the
	// header strip. Empty omits the slot.
	HeaderContent() string

	// Footer is the optional label centred in the bordered body's
	// bottom edge — k9s-style symmetry with Title in the top edge.
	// Pages that have nothing to surface return "" and the bottom
	// border renders as a plain rule. Used for ambient state that
	// belongs framed but doesn't deserve a body-line of its own,
	// like the silences page's "next refresh" countdown.
	Footer() string

	// Bindings returns the page's hint-strip actions (already
	// filtered for read-only mode by the registry, if applicable).
	// The app shell rebuilds the hint strip on every render from
	// the top-of-stack page.
	Bindings() []action.Action
}

// ScopeChangedMsg announces that the user picked a new tenant
// scope via the numeric quick-switch (`<0>` all / `<1>`..`<9>`
// configured tenants) or, in the future, via the tenant picker
// modal. List pages observe it to filter their per-tenant
// snapshots and to update their Title's `(<scope>)` segment.
// Pages that don't care ignore the message.
type ScopeChangedMsg struct {
	// Scope is "all" when every tenant is selected; otherwise
	// it's the configured backend name. Multi-tenant subsets
	// arrive as a comma-joined string ("prod,staging") so the
	// title and per-page filter logic don't need a new shape.
	Scope string
}

// GoToFirstRowMsg is the table-context "first row" signal —
// fired by the dispatcher when the user types the `gg` chord
// (registered at LayerTable in the wiring layer). List pages
// consume it in their Update to scroll the cursor home; pages
// that don't bind it ignore it. Defined here, not in keys/, so
// pages don't have to import keys just for the message type.
type GoToFirstRowMsg struct{}

// ClearMarksMsg is the global "drop every mark on the focused
// page" signal — fired by the dispatcher on `Ctrl+\` (registered
// at LayerGlobal in the wiring layer). List pages with a marks
// map (alerts, silences) handle it; everyone else ignores it. The
// receiving page typically flashes "marks cleared" when the pre-
// clear count was non-zero so the user sees confirmation, and
// silently no-ops when no marks were active. Defined here so
// pages don't have to import keys/ just for the message type.
type ClearMarksMsg struct{}

// QuitRequestedMsg is the precursor every quit path emits instead
// of a bare tea.Quit Cmd: the `q` / Ctrl+C bindings, the `:q`
// cmdbar handler. The App's handleLifecycle consumes it, walks
// the page stack invoking Close() on each (so cancelBulk /
// cancelEditorUpdate / silence-form cancel funcs fire), and
// emits tea.Quit as the final Cmd so bubbletea stops.
//
// bubbletea's runtime intercepts tea.QuitMsg before Update so
// returning tea.Quit directly from a binding would skip the page-
// stack tear-down — workers from in-flight bulk fanouts / editor
// writes / status fetches would outlive the program until their
// HTTP timeout elapses. Going through this precursor makes the
// cleanup observable inside Update.
type QuitRequestedMsg struct{}

// RefreshRequestedMsg is the typed message a page emits when the
// user presses `r` to bypass the poll tick. The App routes it to
// the wiring layer's refresh func, which
// pokes the matching pollers via Refresh(). Resource is the bucket
// label set on poll.Options.Resource ("alerts", "silences", …);
// Scope mirrors the page's active scope ("all" / single tenant /
// comma-joined subset). Pages that don't care about the refresh
// loop can ignore the message — only the wiring layer consumes
// it. Defined here so pages don't import poll just for the type.
type RefreshRequestedMsg struct {
	Resource string
	Scope    string
}

// PollAwarePage is the optional Page extension a page implements
// to opt in to resource-filtered cache replay on push. Pages that
// consume poll.DataMsg with a known resource label list every
// label they react to (the silences page returns ["silences"],
// the alerts page returns ["alerts"]; a page that unions multiple
// resources returns each label). Implementing the interface AND
// returning an empty slice means "I don't want any cached payload"
// — useful for pages whose DataMsg branch is vestigial.
//
// Pages that don't implement this interface receive every cached
// payload during replay; production pages should always opt in to
// keep the replay surface tight.
type PollAwarePage interface {
	PollResources() []string
}

// TimeFormatChangedMsg announces a flip of the app-global time-
// format toggle. Flipping it on the alerts page also flips silences
// and the alert-detail summary so the user sees one consistent time
// treatment across views. Defined here so pages don't have to import
// keys/ or this file's siblings just for the routed tea.Msg; the
// format vocabulary itself lives in timerender.
type TimeFormatChangedMsg struct {
	Format timerender.Format
}

// StateFormatToggleMsg is a page-emitted request to flip the
// app-global state-breakdown density (full ↔ compact). Unlike the
// `t` time toggle — a dispatcher-owned global — the state toggle's
// key (`Shift+T`) is a page binding on the alerts list and group
// detail, so the page can't reach the canonical value to flip it.
// It asks the App to flip instead (mirroring RefreshRequestedMsg's
// page→App shape): the App owns the truth, so a page that was below
// the stack during an earlier toggle still flips from the current
// value rather than its own possibly-stale copy.
type StateFormatToggleMsg struct{}

// StateFormatChangedMsg announces a flip of the app-global state-
// breakdown density so the alerts list and group-detail instance
// list render the same way. Emitted by the App in response to a
// StateFormatToggleMsg; the format vocabulary lives in stateformat.
type StateFormatChangedMsg struct {
	Format stateformat.Format
}

// pushPageMsg requests a push of the page produced by Factory.
// The factory shape (instead of a Page value) lets the page's
// Init run inside the App's Update — required by bubbletea
// convention so the returned Cmd reaches the program loop.
type pushPageMsg struct {
	Factory func() Page
}

// popPageMsg requests popping the top of the page stack. Popping
// when the stack has one or zero pages is a no-op so a stray Esc
// at the home view never crashes.
type popPageMsg struct{}

// replacePageMsg replaces the top-of-stack page with the page
// produced by Factory. Used by transitions that swap the active
// view without growing the stack (e.g. tenant picker → reopen the
// invoking view with the new tenant set).
type replacePageMsg struct {
	Factory func() Page
}

// PushPage returns a Cmd that requests pushing a new page. The
// factory is captured by reference so the page is constructed
// inside the App's Update cycle when the message is delivered.
// Public so pages and tests can request transitions without
// reaching into app's internals.
func PushPage(factory func() Page) tea.Cmd {
	return func() tea.Msg { return pushPageMsg{Factory: factory} }
}

// PopPage returns a Cmd that requests popping the top page.
func PopPage() tea.Cmd {
	return func() tea.Msg { return popPageMsg{} }
}

// ReplacePage returns a Cmd that requests replacing the top page
// with the factory's output.
func ReplacePage(factory func() Page) tea.Cmd {
	return func() tea.Msg { return replacePageMsg{Factory: factory} }
}

// AutoPopMsg marks every message that should trigger the App to
// pop the top page after delivering the message to the parent.
// Forms (silence creation, future Mimir config edit, …) emit
// submitted / cancelled messages tagged with this interface so
// the App pops the form off the stack and forwards the result to
// the parent — analogous to how modal.ResultMsg drives modal
// auto-close.
//
// IsAutoPop is the marker method. Empty body forces explicit
// satisfaction so an unrelated tea.Msg can't accidentally match.
type AutoPopMsg interface {
	IsAutoPop()
}

// InputCapturePage is the optional interface a page implements
// when it wants every keystroke routed to it directly, bypassing
// the dispatcher's LayerGlobal bindings (q, :, /, ?, 0-9). Same
// precedence the modal and prompt slots already get; forms opt
// in so users can type those characters into their fields.
// Pages that don't implement it dispatch as before.
type InputCapturePage interface {
	Page
	// CapturesInput returns true when raw-key routing is active.
	// Called per-keystroke; pages typically return a constant.
	CapturesInput() bool
}
