// SPDX-License-Identifier: Apache-2.0

// Package silence renders the silence-creation / -edit form. v0.1
// composes bubbles' textinput / textarea models for the per-field
// chrome (cursor, placeholder, blur/focus states) and keeps a thin
// wrapper for cross-field navigation, validation, and the
// CreateSilence / UpdateSilence verb selection.
//
// Submit calls a backend.Writer via the injected Client interface.
// Success emits SubmittedMsg (auto-pop); failure flashes the error
// and stays on the form so the user can correct and re-submit.
package silence

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Client is the writeable-silences surface the form and the
// silences list page share. The form calls Create / Update (it
// can land in either mode per Options.EditID); the silences page
// also calls ExpireSilence on `x` / `Ctrl+X`. The form never
// expires anything, so ExpireSilence is a cosmetic member of the
// form's contract — that cost is preferred over carrying a
// parallel narrower interface plus the map-narrowing projection
// helpers Go's lack of map-value covariance would otherwise
// require. Allows tests to inject a fake without booting the
// real backend wiring; testutil.FakeSilenceClient already
// satisfies the three-method surface.
type Client interface {
	CreateSilence(ctx context.Context, spec backend.SilenceSpec) (string, error)
	UpdateSilence(ctx context.Context, id string, spec backend.SilenceSpec) error
	ExpireSilence(ctx context.Context, id string) error
}

// SubmittedMsg is emitted on a successful submit. ID carries the
// silence ID returned by the backend so the caller (typically
// the silences list page) can hint, navigate, etc. Updated is
// true when the submit hit UpdateSilence (edit mode) rather than
// CreateSilence — lets parent pages flash "updated" vs.
// "created" without re-deriving which verb fired. Implements
// app.AutoPopMsg so the App pops the form off the stack on
// receipt and routes the message to the parent.
type SubmittedMsg struct {
	ID      string
	Updated bool
}

// IsAutoPop satisfies app.AutoPopMsg.
func (SubmittedMsg) IsAutoPop() {}

// CancelledMsg is emitted on Esc with no submission. Implements
// app.AutoPopMsg so the form auto-closes the same way as on
// submit; the parent page can flash a hint if it wants.
type CancelledMsg struct{}

// IsAutoPop satisfies app.AutoPopMsg.
func (CancelledMsg) IsAutoPop() {}

// BulkSubmittedMsg is emitted on submit when the form was opened in
// bulk mode (Options.Bulk = true). It carries the metadata the user
// filled in once — the parent page is responsible for substituting
// per-target matchers and dispatching one CreateSilence per marked
// alert. No Matchers field: bulk mode never collects them. Implements
// app.AutoPopMsg so the form auto-pops on submit; the page handles
// fan-out from there.
type BulkSubmittedMsg struct {
	Comment  string
	StartsAt time.Time
	EndsAt   time.Time
	Creator  string
}

// IsAutoPop satisfies app.AutoPopMsg.
func (BulkSubmittedMsg) IsAutoPop() {}

// fieldIndex enumerates the form's input slots so Tab navigation
// can walk them in display order. fieldTenant sits at position 0
// per ADR-0011 — the form owns its tenant selection and renders
// it as the first row, above Matchers. The row is disabled (skipped
// by cycleFocus, no leading marker) when the form has only one
// client, when it's in edit mode (a silence cannot move between
// tenants in the AM v2 API), or when bulk mode is active (the
// Targets banner replaces it entirely).
type fieldIndex int

const (
	fieldTenant fieldIndex = iota
	fieldMatchers
	fieldStarts
	fieldEnds
	fieldCreator
	fieldComment
	numFields
)

// Form is the silence-creation / silence-edit page. Implements
// app.Page. Mode is selected by editID: empty → CreateSilence on
// submit; non-empty → UpdateSilence(editID, spec) on submit.
type Form struct {
	// clients is the writeable backend map keyed by tenant name.
	// Submit routes to clients[tenant]; the Tenant row's Enter
	// opens a picker over the keys (sorted alphabetically). Per
	// ADR-0011 the form takes the full map rather than a single
	// resolved Client so the user — not the caller — picks the
	// write target.
	clients map[string]Client
	// tenant is the currently-selected tenant name. Mutated only
	// by a PickerSubmittedMsg landing on the form; empty in bulk
	// mode (the banner is the source of truth there).
	tenant string
	styles *theme.Styles
	now    func() time.Time

	// matchers is the multi-line free-form buffer holding one
	// matcher per line (`name=value`, `name=~regex`, …). Bubbles'
	// textarea handles cursor / wrap / paste / Home/End for free.
	matchers textarea.Model
	// Single-line scalar fields. Bubbles' textinput supplies
	// cursor + placeholder + blur/focus styling.
	starts  textinput.Model
	ends    textinput.Model
	creator textinput.Model
	comment textinput.Model

	// editID is the silence ID to update on submit. Empty means
	// the form is in create mode; non-empty switches the submit
	// branch to UpdateSilence and the title to "edit silence <id>".
	editID string

	// bulk is the bulk-create mode flag wired from Options.Bulk.
	// True hides matchers, skips matcher validation, renders the
	// banner instead of the textarea, and routes submit through
	// BulkSubmittedMsg.
	bulk bool
	// bulkBanner is the descriptive string rendered in place of the
	// matchers buffer when bulk is true. See Options.BulkBanner.
	bulkBanner string

	focus fieldIndex
	err   string // last submit error; cleared on next keystroke

	// submitting is true between the user pressing Ctrl+S and the
	// async round-trip's submitDoneMsg landing. While set, a second
	// Ctrl+S is dropped (with a flash hint) so a slow tenant cannot
	// be double-submitted by an impatient operator hammering the
	// key. submitDoneMsg clears it.
	submitting bool

	// submitGen is bumped on every Ctrl+S. The active submit's
	// goroutine carries the value at the time it was queued; on
	// arrival applySubmitDone discards the message if the
	// generation no longer matches — the operator pressed Esc and
	// the form was popped before this round-trip completed, so the
	// result must not be projected onto whatever page is on top now
	// (or a freshly-pushed second silence form).
	submitGen int

	// cancelSubmit cancels the context handed to the in-flight
	// Create/UpdateSilence call so Close() (form pop / app shutdown)
	// aborts the request instead of letting the goroutine outlive
	// the form. Guarded by mu because the submit goroutine clears
	// it while Close() (Update goroutine) reads it. Nil when no
	// submit is in flight.
	mu           sync.Mutex
	cancelSubmit context.CancelFunc

	// submitCtx is the parent ctx the Create/UpdateSilence call
	// derives from. Mirrors Options.SubmitCtx — see the doc there
	// for the rationale. Nil means "no parent pinned"; submit()
	// falls back to context.Background() so single-shot tests that
	// don't care about app-level propagation stay green.
	submitCtx context.Context //nolint:containedctx // submit write ctx, plumbed once at construction.
}

// Options captures the dependency surface. The prefill fields
// (Matchers / Comment / EndsAt / EditID) are independently
// optional — they exist so a caller pushing the form on `s` from
// an alert / group can pre-populate matchers, and so a caller
// pushing on `e` against an existing silence can hand the form
// every field plus the EditID that switches submit to
// UpdateSilence. None of them are required for the create-from-
// scratch path.
type Options struct {
	// Clients is the writeable backend map the form picks from on
	// Enter against the Tenant row. The caller hands the whole map
	// (typically the page's p.clients) so scope filtering doesn't
	// gate write targets — picking a tenant out-of-scope is a
	// legitimate operator action. Per ADR-0011.
	Clients map[string]Client
	// Tenant is the initial selection. Required when Clients is
	// non-empty and Bulk is false; ignored in bulk mode (the banner
	// carries the per-target breakdown there).
	Tenant string
	Styles *theme.Styles
	// Now injects the clock used to default StartsAt and resolve
	// duration shorthands like "2h". nil falls back to time.Now.
	Now func() time.Time
	// Creator is the default value for the creator field —
	// typically $USER.
	Creator string
	// Matchers, when non-empty, prefill the matchers buffer
	// formatted one per line in the same syntax the user types
	// manually (`name=value`, `name=~regex`, …). Round-trips
	// through parseMatchers so editing via Tab + backspace works
	// without a special path.
	Matchers []backend.Matcher
	// Comment, when non-empty, prefills the comment field.
	Comment string
	// EndsAt, when non-zero, prefills the ends field with an
	// RFC3339 timestamp. The form keeps the existing "2h"
	// shorthand default when EndsAt is the zero value.
	EndsAt time.Time
	// EditID switches submit from CreateSilence to UpdateSilence.
	// Empty → create mode (default). Non-empty → edit mode; the
	// form's title and SubmittedMsg both echo the id so callers
	// can flash "silence updated: <id>".
	EditID string

	// Bulk switches the form into bulk-create mode. The matchers
	// buffer is hidden, the matchers-required validation is skipped,
	// the banner string below is rendered where the buffer would
	// have lived, and submit emits BulkSubmittedMsg instead of
	// calling Client.CreateSilence. The page that opened the form
	// owns the per-target matcher substitution and the fan-out.
	// Client may be nil in bulk mode (the form never calls it). Bulk
	// is mutually exclusive with EditID — bulk-edit is out of scope
	// (see docs/design/bulk-silence.md "Out of scope").
	Bulk bool

	// BulkBanner is the descriptive line rendered where the matchers
	// buffer would have rendered when Bulk is true. The page formats
	// this string with the target count and tenant breakdown so the
	// user sees what their submit will fan out to (e.g. "applies to
	// 5 alerts across 2 tenants — each silenced with its own
	// labels"). Ignored when Bulk is false.
	BulkBanner string

	// BlankEnds skips the "2h" default in the Ends field, leaving
	// it empty so the user must type a fresh duration. Used by the
	// recreate-expired entry point so a stray Ctrl+S does not
	// resurrect the silence with a placeholder duration.
	BlankEnds bool

	// FocusEnds lands initial focus on the Ends field instead of
	// the default Matchers field. Used by the recreate-expired
	// entry point where Matchers / Comment are already prefilled
	// and the only field the user still has to set is Ends.
	FocusEnds bool

	// SubmitCtx is the parent ctx the Create/UpdateSilence call
	// inherits. Cancelling cancels the in-flight write — keeps the
	// form in lockstep with the alerts/silences pages whose
	// BulkCtx / EditorCtx already chain through cmd.Context(), so
	// app-level shutdown propagates through the ctx (not only
	// through Close). nil falls back to context.Background() —
	// kept so tests that don't pin the parent stay green.
	SubmitCtx context.Context //nolint:containedctx // submit write ctx, plumbed once at construction.
}

// matchersHeight is the number of visible rows reserved for the
// matchers textarea. Six is enough for the typical 2-3-line
// silence without forcing a scroll.
const matchersHeight = 6

// New constructs a Form. Prefill fields on opts (Matchers,
// Comment, EndsAt, EditID) are applied only when set; an empty
// Options yields the create-from-scratch shape.
func New(opts Options) *Form {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	matchers := textarea.New()
	matchers.Prompt = ""
	matchers.Placeholder = "alertname=HighCPU\nseverity=critical"
	matchers.SetHeight(matchersHeight)
	matchers.ShowLineNumbers = false
	flattenTextareaBlur(&matchers)
	// Skip the matchers prefill in bulk mode — the buffer is hidden
	// and parseSpec ignores it; pre-populating would only leak state
	// into a future non-bulk reuse.
	if !opts.Bulk && len(opts.Matchers) > 0 {
		matchers.SetValue(formatMatchers(opts.Matchers))
	}

	starts := newInput("now")
	ends := newInput("2h")
	switch {
	case opts.BlankEnds:
		// Leave value empty — recreate path forces the user to
		// type a fresh duration. Placeholder still hints "2h" so
		// the shape is discoverable; parseEndsAt rejects empty at
		// submit time to make this an actual guard, not a hint.
	case !opts.EndsAt.IsZero():
		ends.SetValue(opts.EndsAt.UTC().Format(time.RFC3339))
	default:
		ends.SetValue("2h")
	}
	creator := newInput("$USER")
	if opts.Creator != "" {
		creator.SetValue(opts.Creator)
	}
	comment := newInput("ack while patching")
	if opts.Comment != "" {
		comment.SetValue(opts.Comment)
	}

	f := &Form{
		clients:    opts.Clients,
		tenant:     opts.Tenant,
		styles:     opts.Styles,
		now:        now,
		matchers:   matchers,
		starts:     starts,
		ends:       ends,
		creator:    creator,
		comment:    comment,
		editID:     opts.EditID,
		bulk:       opts.Bulk,
		bulkBanner: opts.BulkBanner,
		submitCtx:  opts.SubmitCtx,
	}
	// Default focus is fieldMatchers (the iota+1 slot). Tenant is
	// the visual first row but not the keyboard-first row: the user
	// opens the form to type matchers, and only tabs back to Tenant
	// when they want to change the write target. Bulk mode hides
	// matchers entirely so focus starts on Starts; FocusEnds layers
	// on top for the recreate-expired entry point.
	f.focus = fieldMatchers
	if f.bulk {
		f.focus = fieldStarts
	}
	if opts.FocusEnds {
		f.focus = fieldEnds
	}
	_ = f.activeFocus()
	return f
}

// newInput constructs a textinput.Model with the form's shared
// shape: no built-in prompt (the row label provides one), the
// supplied placeholder for empty state, and no character limit.
//
// Typed text is forced to the body's default foreground in both
// focused and blurred states so a filled row never reads as
// stale — bubbles' default paints blurred text in dim grey,
// which collides with the form's focus marker (a leading `▸`
// plus the accent-tinted label) and made every blurred-but-
// filled row look disabled.
//
// Placeholder dim is deliberately kept on both states per
// ADR-0012 so the operator can distinguish an empty field
// ("$USER", "2h", …) from one carrying a real value at a
// glance. The placeholder colour is foreground-only — no
// background paint — so the chrome-on-default-bg rule is
// preserved.
func newInput(placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	s := in.Styles()
	s.Blurred.Text = s.Focused.Text
	in.SetStyles(s)
	return in
}

// flattenTextareaBlur strips the bubbles defaults that would
// fight the form's focus chrome. Two slots are flattened:
//   - Text in both focused and blurred states, so typed
//     matchers stay at default fg whichever row owns focus
//     (bubbles' blurred default dims text and made filled
//     rows read as stale);
//   - CursorLine in both states, so the active line never
//     paints a background stripe behind the buffer (the
//     chrome-on-default-bg rule).
//
// Placeholder is intentionally left at the bubbles default
// per ADR-0012 — the dim foreground is what distinguishes an
// empty matchers buffer from a populated one. The default is
// foreground-only ("alertname=HighCPU\nseverity=critical" in
// grey), no background paint, so the no-stripe rule still
// holds.
func flattenTextareaBlur(m *textarea.Model) {
	s := m.Styles()
	bare := lipgloss.NewStyle()
	s.Focused.Text = bare
	s.Blurred.Text = bare
	s.Focused.CursorLine = bare
	s.Blurred.CursorLine = bare
	m.SetStyles(s)
}

// Init implements app.Page.
func (*Form) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (f *Form) Close() tea.Cmd {
	// Cancel any in-flight Create/UpdateSilence so a slow tenant
	// doesn't keep writing after the user pops the form. Without
	// this, an Esc-then-page-swap leaves the goroutine running with
	// a never-cancelled ctx, and the silence is created/updated on
	// the server with no operator confirmation in the TUI.
	f.mu.Lock()
	cancel := f.cancelSubmit
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// CapturesInput implements app.InputCapturePage so the form
// receives `q`, `:`, `/`, `?`, `0`-`9` as text input rather than
// having the dispatcher consume them. Esc still cancels via the
// form's own handler.
func (*Form) CapturesInput() bool { return true }

// Crumb implements app.Page.
func (*Form) Crumb() string { return "silence" }

// Title implements app.Page. Reads "new silence" in create mode,
// "edit silence <id>" in edit mode, and "bulk silence" in bulk
// mode so the user always knows which verb the submit will fire.
// The bulk form's banner carries the per-target count breakdown,
// keeping the title slot clean.
func (f *Form) Title() string {
	if f.bulk {
		return "bulk silence"
	}
	if f.editID != "" {
		return "edit silence " + f.editID
	}
	return "new silence"
}

// HeaderContent implements app.Page. Empty so the App's
// subtitle slot collapses — Title() already labels the panel
// border with "new silence" / "edit silence <id>", a subtitle
// echo would just duplicate it.
func (*Form) HeaderContent() string { return "" }

// Footer implements app.Page. Form doesn't surface ambient
// state in the bottom border.
func (*Form) Footer() string { return "" }

// Bindings implements app.Page.
func (*Form) Bindings() []action.Action {
	return []action.Action{
		{Key: "Tab", Description: "next field", View: "silence-form"},
		{Key: "Shift+Tab", Description: "prev field", View: "silence-form"},
		{Key: "Enter", Description: "pick tenant (on Tenant row)", View: "silence-form"},
		{Key: "Ctrl+S", Description: "submit", View: "silence-form"},
	}
}

// Update implements app.Page. Cross-field navigation and submit
// land here directly; every other message — printable keys,
// cursor keys, paste, backspace, Ctrl+U, plus the non-key
// messages that drive cursor blink (cursor.BlinkMsg) and paste
// completion — forwards to the focused bubbles input. Without
// the non-key forward the cursor would never blink because the
// blink Cmd Focus() returns produces a tea.Msg the form
// otherwise swallows.
func (f *Form) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if m, ok := msg.(submitDoneMsg); ok {
		return f, f.applySubmitDone(m)
	}
	// Picker results land here when the user picks (or cancels) a
	// tenant on the Tenant row's Enter. Submitted updates f.tenant;
	// cancelled is a silent no-op. Either way focus stays on the
	// Tenant row so a tab walks the remaining fields predictably.
	// Origin gates the handler so a foreign picker (e.g. a future
	// global picker forwarded through forwardToTop) cannot reach in
	// and stomp the active tenant.
	if m, ok := msg.(modal.PickerSubmittedMsg); ok && m.Origin == pickerOrigin {
		if len(m.Selections) > 0 {
			f.tenant = m.Selections[0]
		}
		return f, nil
	}
	if m, ok := msg.(modal.PickerCancelledMsg); ok && m.Origin == pickerOrigin {
		return f, nil
	}
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "tab":
			cmd := f.cycleFocus(1)
			return f, cmd
		case "shift+tab":
			cmd := f.cycleFocus(-1)
			return f, cmd
		case "esc":
			return f, func() tea.Msg { return CancelledMsg{} }
		case "ctrl+s":
			cmd := f.submit()
			return f, cmd
		case "enter":
			// Enter on the Tenant row opens the tenant picker; on
			// every other row it falls through to the focused field
			// (textarea grows a newline; textinput is a no-op).
			if f.focus == fieldTenant && !f.tenantDisabled() {
				cmd := f.openTenantPicker()
				return f, cmd
			}
		}
	}
	cmd := f.forwardToFocused(msg)
	return f, cmd
}

// pickerOrigin tags every PickerSubmittedMsg / PickerCancelledMsg
// the form's tenant picker emits. The App's lifecycle router only
// short-circuits picker results carrying app.PickerOriginScope
// (the Ctrl+T global picker) — everything else, including this
// tag, is forwarded to the top page so the form's Update consumes
// it. Originator-tagging beats a private wrapped message because
// the picker submit Cmd is fired by bubbletea directly into the
// App and the form has no intercept point before forwardToTop.
const pickerOrigin = "silence-form-tenant"

// openTenantPicker returns a Cmd that asks the App to push a
// single-select picker over every key in f.clients. The picker
// list is sorted alphabetically so the order is stable across
// runs / tests. Selection routes back as modal.PickerSubmittedMsg
// (handled in Update). The form deliberately passes the full map
// rather than intersecting with the page's scope: per ADR-0011
// scope is a viewing filter, deliberate writes shouldn't be gated
// by what the operator happens to be looking at.
func (f *Form) openTenantPicker() tea.Cmd {
	names := f.sortedTenantNames()
	return app.OpenModal(func() modal.Modal {
		return modal.NewPicker("Select tenant", names, modal.PickerSingle).
			WithOrigin(pickerOrigin)
	})
}

// sortedTenantNames returns f.clients keys in stable alphabetical
// order. Extracted from openTenantPicker so tests can assert the
// sort contract without reaching through the picker's modal envelope.
func (f *Form) sortedTenantNames() []string {
	names := make([]string, 0, len(f.clients))
	for t := range f.clients {
		names = append(names, t)
	}
	sort.Strings(names)
	return names
}

// tenantDisabled reports whether the Tenant row is read-only.
// Three independent triggers:
//   - bulk mode: row omitted entirely; checking the flag here
//     keeps the cycleFocus / Enter handler consistent with the
//     renderer's omission.
//   - editID set: edit mode can't move a silence between tenants.
//   - fewer than two clients: nothing meaningful to pick.
func (f *Form) tenantDisabled() bool {
	if f.bulk {
		return true
	}
	if f.editID != "" {
		return true
	}
	return len(f.clients) < 2
}

// forwardToFocused dispatches the message to whichever bubbles
// input is currently focused. Each model's Update returns a
// fresh copy (value receiver), so the slot is reassigned in
// place. Accepts tea.Msg (not just KeyPressMsg) so cursor blink
// ticks and paste completions reach the focused field too.
func (f *Form) forwardToFocused(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch f.focus {
	case fieldTenant:
		// Tenant has no bubbles input — it's a read-out for the
		// active selection, modified only via the picker. Drop the
		// message rather than routing it anywhere; the cursor blink
		// loop is keyed off the other fields' Focus() Cmds.
	case fieldMatchers:
		f.matchers, cmd = f.matchers.Update(msg)
	case fieldStarts:
		f.starts, cmd = f.starts.Update(msg)
	case fieldEnds:
		f.ends, cmd = f.ends.Update(msg)
	case fieldCreator:
		f.creator, cmd = f.creator.Update(msg)
	case fieldComment:
		f.comment, cmd = f.comment.Update(msg)
	case numFields:
		// Sentinel — never the active focus.
	}
	return cmd
}

// cycleFocus walks focus by delta (typically ±1), blurring the
// outgoing field and focusing the incoming one. Returns the
// Cmd Focus emits (cursor blink schedule) so the program loop
// drives the new field's blink timer.
//
// Two kinds of fields are skipped on the way:
//   - fieldMatchers in bulk mode (the textarea is hidden, the
//     Targets banner is non-focusable);
//   - fieldTenant when tenantDisabled() — single-client / edit-mode /
//     bulk; the row either isn't rendered or renders read-only.
//
// The skip loop runs at most numFields-1 times to guarantee
// termination even in a future shape where every slot is disabled
// (defensive — not reachable today).
func (f *Form) cycleFocus(delta int) tea.Cmd {
	f.activeBlur()
	for range int(numFields) {
		f.focus = (f.focus + fieldIndex(delta) + numFields) % numFields
		if !f.focusDisabled() {
			break
		}
	}
	return f.activeFocus()
}

// focusDisabled reports whether the slot at f.focus is one the
// cycle must skip. Mirrors the renderer's omission rules so Tab
// never lands on a row the user can't actually edit. The other
// fields (Starts/Ends/Creator/Comment/numFields) are always
// focusable / sentinel, so they take the default-false branch.
func (f *Form) focusDisabled() bool {
	switch f.focus {
	case fieldTenant:
		return f.tenantDisabled()
	case fieldMatchers:
		return f.bulk
	case fieldStarts, fieldEnds, fieldCreator, fieldComment, numFields:
		return false
	}
	return false
}

// activeFocus calls Focus on the field at the current index.
// fieldTenant has no bubbles input — the row is a static display
// of the active selection, so the focus call is a no-op there.
func (f *Form) activeFocus() tea.Cmd {
	switch f.focus {
	case fieldTenant:
		return nil
	case fieldMatchers:
		return f.matchers.Focus()
	case fieldStarts:
		return f.starts.Focus()
	case fieldEnds:
		return f.ends.Focus()
	case fieldCreator:
		return f.creator.Focus()
	case fieldComment:
		return f.comment.Focus()
	case numFields:
	}
	return nil
}

// activeBlur calls Blur on the field at the current index.
func (f *Form) activeBlur() {
	switch f.focus {
	case fieldTenant:
		// No bubbles input behind this row — nothing to blur.
	case fieldMatchers:
		f.matchers.Blur()
	case fieldStarts:
		f.starts.Blur()
	case fieldEnds:
		f.ends.Blur()
	case fieldCreator:
		f.creator.Blur()
	case fieldComment:
		f.comment.Blur()
	case numFields:
	}
}

// submitDoneMsg is the result of an async CreateSilence /
// UpdateSilence round-trip. Update routes it back through f.fail or
// emits SubmittedMsg on the next tick. Kept private — the form is
// the only producer and consumer. gen pins the submit attempt this
// message belongs to; if the form was popped (Esc) and a fresh form
// pushed, the generation no longer matches and the message is
// discarded so it can't auto-pop the new form with stale content.
type submitDoneMsg struct {
	gen     int
	id      string
	updated bool
	err     error
}

// submit parses the buffers and routes to one of three shapes:
// bulk-create emits BulkSubmittedMsg (the page fans out per-target);
// edit calls UpdateSilence; create calls CreateSilence.
//
// Validation runs synchronously (cheap, no I/O). The HTTP round-trip
// runs inside the returned tea.Cmd so bubbletea executes it on its
// own goroutine — without this indirection, a slow tenant would
// freeze the Update loop for up to the transport timeout. Result is
// posted as submitDoneMsg and translated by Update.
//
// Re-entry guard: a second Ctrl+S while a submit is already in
// flight flashes a hint and drops the new attempt. Without this an
// impatient operator on a slow tenant would post duplicate
// CreateSilence requests by reflex; the existing submit is going to
// land in the transport timeout anyway.
func (f *Form) submit() tea.Cmd {
	if f.submitting {
		return flashFn("silence: submit already in flight")
	}
	spec, err := f.parseSpec()
	if err != nil {
		return f.fail(err.Error())
	}
	if f.bulk {
		// Clients may legitimately be nil/empty in bulk mode — the
		// page owns dispatch, the form just collects metadata.
		return func() tea.Msg {
			return BulkSubmittedMsg{
				Comment:  spec.Comment,
				StartsAt: spec.StartsAt,
				EndsAt:   spec.EndsAt,
				Creator:  spec.CreatedBy,
			}
		}
	}
	// Resolve the write target from the active tenant. Defensive:
	// in normal flow f.tenant is set by the caller (initial pick)
	// or by a PickerSubmittedMsg landing on the form, and the
	// resolved client is non-nil. An empty tenant or a missing key
	// is unreachable through the UI but worth refusing loudly so a
	// future refactor that loses the wiring fails closed.
	if f.tenant == "" {
		return f.fail("no tenant selected")
	}
	client, ok := f.clients[f.tenant]
	if !ok || client == nil {
		return f.fail("no client for tenant " + f.tenant)
	}
	f.submitGen++
	gen := f.submitGen
	f.submitting = true
	// Wire a cancellable ctx so Close() (form pop / app shutdown)
	// aborts the request instead of letting the goroutine outlive
	// the form. The prior code used context.Background(), which
	// meant a slow tenant kept writing even after the user Esc'd
	// out — leaving an orphan silence that the operator never sees
	// confirmed.
	f.mu.Lock()
	if f.cancelSubmit != nil {
		// A previous submit was somehow still in flight; cancel it
		// so we don't have two writes racing on the same form.
		f.cancelSubmit()
	}
	// Parent on Options.SubmitCtx when wired so app-level
	// cancellation propagates through the ctx (not only via
	// Close). nil falls back to context.Background() — kept so
	// tests / callers that don't pin a parent still work.
	parent := f.submitCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	f.cancelSubmit = cancel
	f.mu.Unlock()
	clearCancel := func() {
		f.mu.Lock()
		f.cancelSubmit = nil
		f.mu.Unlock()
		cancel()
	}
	if f.editID != "" {
		id := f.editID
		return func() tea.Msg {
			defer clearCancel()
			err := client.UpdateSilence(ctx, id, spec)
			return submitDoneMsg{gen: gen, id: id, updated: true, err: err}
		}
	}
	return func() tea.Msg {
		defer clearCancel()
		id, err := client.CreateSilence(ctx, spec)
		return submitDoneMsg{gen: gen, id: id, err: err}
	}
}

// applySubmitDone routes a submitDoneMsg back into the form. Stale
// generations (the user hit Esc and a new form may now be live) are
// silently dropped — the message must not auto-pop a different
// form or flash a stale "silence created" on whatever page is on
// top. Errors flash + keep the form open; success emits the
// appropriate auto-pop message. Runs on the Update goroutine so
// f.err / f.fail mutations are race-free.
func (f *Form) applySubmitDone(m submitDoneMsg) tea.Cmd {
	if m.gen != f.submitGen {
		return nil
	}
	f.submitting = false
	if m.err != nil {
		if errors.Is(m.err, context.Canceled) {
			// Cancellation is shutdown noise, not a backend failure.
			// The submit goroutine returned because Close() / SIGTERM
			// cancelled SubmitCtx — the user sees no misleading flash
			// and the page-pop has already auto-popped the form.
			return nil
		}
		return f.fail(m.err.Error())
	}
	id := m.id
	updated := m.updated
	return func() tea.Msg { return SubmittedMsg{ID: id, Updated: updated} }
}

// flashFn is a tiny helper for surfacing a single ephemeral hint
// without touching f.err — used when the form rejects a keystroke
// (e.g. duplicate Ctrl+S during an in-flight submit) without
// recording it as the persistent submit error.
func flashFn(text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: footer.FlashWarn, Text: text}
	}
}

// fail records the error on the form and returns a Cmd that
// surfaces it as a flash.
func (f *Form) fail(text string) tea.Cmd {
	f.err = text
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: footer.FlashError, Text: "silence: " + text}
	}
}

// parseSpec converts the field buffers into a backend.SilenceSpec.
// Returns the first validation error encountered. In bulk mode the
// matchers buffer is hidden and the resulting spec leaves Matchers
// empty; the parent page substitutes per-target matchers at fan-out.
func (f *Form) parseSpec() (backend.SilenceSpec, error) {
	matchers, err := parseMatchers(f.matchers.Value())
	if err != nil {
		return backend.SilenceSpec{}, err
	}
	if !f.bulk && len(matchers) == 0 {
		return backend.SilenceSpec{}, errors.New("at least one matcher is required")
	}
	starts, err := parseTimeOrNow(f.starts.Value(), f.now())
	if err != nil {
		return backend.SilenceSpec{}, errors.New("starts: " + err.Error())
	}
	ends, err := parseEndsAt(f.ends.Value(), starts)
	if err != nil {
		return backend.SilenceSpec{}, errors.New("ends: " + err.Error())
	}
	if !ends.After(starts) {
		return backend.SilenceSpec{}, errors.New("ends must be after starts")
	}
	creator := strings.TrimSpace(f.creator.Value())
	if creator == "" {
		return backend.SilenceSpec{}, errors.New("creator is required")
	}
	comment := strings.TrimSpace(f.comment.Value())
	if comment == "" {
		return backend.SilenceSpec{}, errors.New("comment is required")
	}
	return backend.SilenceSpec{
		Matchers:  matchers,
		StartsAt:  starts,
		EndsAt:    ends,
		CreatedBy: creator,
		Comment:   comment,
	}, nil
}

// MatchersFromLabels turns a label-set into equality matchers,
// dropping the synthetic `__name__` key. Sorted by name so a
// prefilled form renders deterministically. Shared between the
// alerts list / alert detail / groups pages so all three build
// the same matchers from the same labels and a future change
// (different ignored keys, different operators) lands in one
// place. Lives here because the silenceform package is the only
// consumer of the output.
func MatchersFromLabels(labels map[string]string) []backend.Matcher {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]backend.Matcher, 0, len(keys))
	for _, k := range keys {
		out = append(out, backend.Matcher{
			Name: k, Value: labels[k], IsEqual: true,
		})
	}
	return out
}

// formatMatchers renders matchers in the same one-per-line syntax
// the user types manually so a prefilled form can be edited
// without a special path. Inverse of parseMatchers — the symmetry
// is asserted by TestForm_FormatMatchersRoundTrip.
func formatMatchers(in []backend.Matcher) string {
	if len(in) == 0 {
		return ""
	}
	parts := make([]string, len(in))
	for i, m := range in {
		parts[i] = m.Name + matcherOp(m) + m.Value
	}
	return strings.Join(parts, "\n")
}

// matcherOp picks the operator symbol for a matcher's IsRegex /
// IsEqual flags. Mirrors parseOneMatcher's table.
func matcherOp(m backend.Matcher) string {
	switch {
	case m.IsRegex && m.IsEqual:
		return "=~"
	case m.IsRegex && !m.IsEqual:
		return "!~"
	case !m.IsRegex && m.IsEqual:
		return "="
	default:
		return "!="
	}
}

// parseMatchers walks one-matcher-per-line input. Operators per
// Prometheus convention: `=`, `!=`, `=~`, `!~`. Leading / trailing
// whitespace is trimmed; blank lines are skipped.
func parseMatchers(in string) ([]backend.Matcher, error) {
	out := make([]backend.Matcher, 0)
	for i, raw := range strings.Split(in, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		m, err := parseOneMatcher(line)
		if err != nil {
			return nil, errLineWrap(i+1, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// parseOneMatcher splits a single matcher line on its leftmost
// operator with two-char operators (`!~`, `=~`, `!=`) winning a
// tie against the single-char `=`. Leftmost-position semantics
// matter for round-trips: a value that itself contains an
// operator (e.g. `foo=a!=b` from `{Name:"foo", Value:"a!=b"}`)
// must split on the first `=`, not on the `!=` later in the
// line. Two-char operators win ties so `foo=~bar` parses as a
// regex match (`=~` at index 3) rather than a literal-equal of
// `~bar` (`=` at the same index). Leading match (`idx == 0`) is
// rejected so a stray `=oops` line still flags as missing-name.
func parseOneMatcher(line string) (backend.Matcher, error) {
	type opDef struct {
		s       string
		isRegex bool
		isEqual bool
	}
	// Two-char ops first so a tie at the same index resolves in
	// their favour (the loop below only updates bestIdx on a
	// strictly-smaller index, never a tie).
	ops := []opDef{
		{s: "!~", isRegex: true, isEqual: false},
		{s: "=~", isRegex: true, isEqual: true},
		{s: "!=", isRegex: false, isEqual: false},
		{s: "=", isRegex: false, isEqual: true},
	}
	bestIdx := -1
	var bestOp opDef
	for _, o := range ops {
		idx := strings.Index(line, o.s)
		if idx <= 0 {
			continue
		}
		if bestIdx == -1 || idx < bestIdx {
			bestIdx = idx
			bestOp = o
		}
	}
	if bestIdx == -1 {
		return backend.Matcher{}, errors.New("missing operator (=, !=, =~, !~)")
	}
	name := strings.TrimSpace(line[:bestIdx])
	value := strings.TrimSpace(line[bestIdx+len(bestOp.s):])
	if name == "" || value == "" {
		return backend.Matcher{}, errors.New("matcher must be name<op>value")
	}
	return backend.Matcher{
		Name: name, Value: value,
		IsRegex: bestOp.isRegex, IsEqual: bestOp.isEqual,
	}, nil
}

// errLineWrap wraps err with a 1-based line number for matcher
// validation messages.
func errLineWrap(line int, err error) error {
	return errors.New("line " + itoa(line) + ": " + err.Error())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// parseTimeOrNow returns now when in is empty / "now"; otherwise
// parses RFC3339.
func parseTimeOrNow(in string, now time.Time) (time.Time, error) {
	in = strings.TrimSpace(in)
	if in == "" || in == "now" {
		return now, nil
	}
	return time.Parse(time.RFC3339, in)
}

// parseEndsAt accepts either a duration shorthand ("2h", "30m")
// relative to base, or an RFC3339 timestamp. Empty input is a
// validation error so the BlankEnds entry point (recreate-expired)
// can't be Ctrl+S'd through with no duration typed — the field's
// "2h" placeholder is a hint, not a default. The legacy `n` and
// `e` flows pre-fill a non-empty value, so they never hit this path.
func parseEndsAt(in string, base time.Time) (time.Time, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return time.Time{}, errors.New("ends is required")
	}
	if d, err := time.ParseDuration(in); err == nil {
		return base.Add(d), nil
	}
	return time.Parse(time.RFC3339, in)
}

// labelWidth is the column width reserved for the field labels
// (`Matchers:`, `Starts:`, …). Eleven cols fit the longest label
// plus the colon plus a space.
const labelWidth = 11

// View implements app.Page. Renders one labeled row per field —
// label on the left, the bubbles input's View on the right —
// with the focused row's label tinted via the theme's accent
// colour and a leading `▸` so the active field is unmissable.
//
// Row order per ADR-0011: Tenant first (omitted in bulk; read-only
// when single-client or in edit mode), then Matchers / Targets,
// then the metadata fields.
func (f *Form) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	inputWidth := max(
		// -2 = leading prefix "▸ " or "  "
		width-labelWidth-2, 10)
	f.matchers.SetWidth(inputWidth)
	f.starts.SetWidth(inputWidth)
	f.ends.SetWidth(inputWidth)
	f.creator.SetWidth(inputWidth)
	f.comment.SetWidth(inputWidth)

	rows := make([]string, 0, int(numFields))
	if !f.bulk {
		rows = append(rows, f.tenantRow(inputWidth))
	}
	rows = append(rows,
		f.matcherSlotRow(),
		f.fieldRow("Starts", fieldStarts, f.starts.View()),
		f.fieldRow("Ends", fieldEnds, f.ends.View()),
		f.fieldRow("Creator", fieldCreator, f.creator.View()),
		f.fieldRow("Comment", fieldComment, f.comment.View()),
	)
	body := strings.Join(rows, "\n")
	if f.err != "" {
		// The hint strip in the top panel already advertises
		// Tab / Shift+Tab / Ctrl+S; the only thing the form
		// itself needs to surface in the body is a recent
		// validation error so the user can see what to fix.
		body += "\n\n" + f.styles.Flash.Error.Render("ERR: "+f.err)
	}
	return lipgloss.NewStyle().Width(width).Render(body)
}

// tenantRow renders the leading Tenant row. The value is the
// current f.tenant (empty string falls back to a "—" placeholder
// so an unselected form is still visually obvious). When the row
// is disabled — single-client deployments and edit mode — the
// renderer drops the leading `▸` marker even when focus happens
// to land here, and falls back to the neutral label style so the
// row reads as informational rather than actionable. Bulk mode
// never reaches this code path (View omits the row outright).
func (f *Form) tenantRow(inputWidth int) string {
	value := f.tenant
	if value == "" {
		value = "—"
	}
	if f.tenantDisabled() {
		return f.disabledRow("Tenant", value)
	}
	// Append a faint "[Enter to change]" hint so the picker affordance
	// is discoverable without the user having to guess. Faint
	// (`\x1b[2m`) is foreground-only — no background paint — and sits
	// next to the value so it doesn't clutter the focus marker. The
	// hint is unconditional (focused or blurred) so a user scanning
	// the form learns the affordance before ever tabbing onto the row.
	//
	// Elided when the row would otherwise wrap: with a long tenant
	// name on a narrow form, appending the hint would push the line
	// past inputWidth and the outer View's Width-wrap would break
	// fieldRow's grid alignment. Trade discoverability for layout
	// integrity at narrow widths — Bindings() still advertises the
	// affordance in the global hint strip.
	const hintBody = enterToChangeHint
	hintCols := lipgloss.Width("  ") + lipgloss.Width(hintBody)
	if lipgloss.Width(value)+hintCols > inputWidth {
		return f.fieldRow("Tenant", fieldTenant, value)
	}
	hint := lipgloss.NewStyle().Faint(true).Render("  " + hintBody)
	return f.fieldRow("Tenant", fieldTenant, value+hint)
}

// enterToChangeHint is the picker-affordance label echoed both in
// the tenant row's inline suffix and (canonically) in Bindings().
// Sharing the literal keeps the two sites in lockstep so a future
// rename can't leave one stale.
const enterToChangeHint = "[Enter to change]"

// disabledRow renders a row with no leading marker and the neutral
// label style — used by Tenant when the row is read-only. Shares
// label padding and multi-line continuation alignment with
// fieldRow so the form's grid stays consistent.
//
// The value cell is dimmed via lipgloss.Faint — a real SGR
// (`\x1b[2m`) that renders as a foreground-only attenuation on
// every modern terminal. ADR-0011 calls for a visual "disabled
// (greyed)" treatment; without it a disabled row would render
// identically to a blurred-but-interactive row (same default fg
// + bold label). Faint is foreground-only by definition so the
// no-background-paint rule still holds, and we deliberately
// don't reach for a new theme role — the dim is a render-time
// affordance, not a palette concept.
func (f *Form) disabledRow(label, value string) string {
	prefix := "  "
	labelStyle := lipgloss.NewStyle().
		Foreground(f.styles.Body.Default.GetForeground()).
		Bold(true)
	labelText := labelStyle.Render(format.PadRight(label+":", labelWidth))
	valueStyle := lipgloss.NewStyle().Faint(true)
	lines := strings.Split(value, "\n")
	for i, ln := range lines {
		dimmed := valueStyle.Render(ln)
		if i == 0 {
			lines[i] = prefix + labelText + dimmed
		} else {
			lines[i] = strings.Repeat(" ", 2+labelWidth) + dimmed
		}
	}
	return strings.Join(lines, "\n")
}

// matcherSlotRow renders the top row of the form. In create / edit
// mode this is the live matchers textarea labelled "Matchers"; in
// bulk mode the textarea is hidden and the slot is filled with the
// non-focusable BulkBanner labelled "Targets" — the banner carries
// the count breakdown the user needs to see what their submit will
// fan out to.
func (f *Form) matcherSlotRow() string {
	if f.bulk {
		return f.fieldRow("Targets", fieldMatchers, f.bulkBanner)
	}
	return f.fieldRow("Matchers", fieldMatchers, f.matchersView())
}

// matchersView wraps f.matchers.View() to work around a bubbles
// textarea bug: placeholderView (textarea.go:1513) only wraps the
// FIRST line of a multi-line Placeholder with the placeholder
// style; lines 2..N render with cursorLine only, which
// flattenTextareaBlur sets to bare — leaving them at the
// terminal's default foreground while line 1 is dim. The result
// is a multi-line hint whose continuation lines visually read as
// typed text. Compose around upstream (no-fork): when the buffer
// is empty, re-style the trailing placeholder lines so the full
// hint reads as one placeholder.
func (f *Form) matchersView() string {
	raw := f.matchers.View()
	if f.matchers.Value() != "" {
		return raw
	}
	if !strings.Contains(f.matchers.Placeholder, "\n") {
		return raw
	}
	// Replicate bubbles' placeholder wrap (textarea.go:1521-1525) so
	// our `plines` matches what bubbles actually rendered — anchoring
	// against the raw `Placeholder` field's newline-split would miss
	// at narrow widths where bubbles word/hard-wraps a long line
	// before splitting, and the substring index would return -1.
	width := f.matchers.Width()
	pwrap := ansi.Hardwrap(ansi.Wordwrap(f.matchers.Placeholder, width, ""), width, true)
	plines := strings.Split(strings.TrimSpace(pwrap), "\n")
	if len(plines) <= 1 {
		return raw
	}
	styles := f.matchers.Styles()
	state := styles.Blurred
	if f.matchers.Focused() {
		state = styles.Focused
	}
	dim := state.Placeholder.Inherit(state.Base).Inline(true)
	lines := strings.Split(raw, "\n")
	for i := 1; i < len(lines) && i < len(plines); i++ {
		phLine := plines[i]
		// bubbles wraps every line with an empty-render prefix
		// (cursor/prompt SGR pair) before the actual placeholder
		// text, so anchor by substring rather than full-line
		// equality. Rewrite the placeholder text in place; the
		// surrounding SGR padding stays as bubbles emitted it.
		idx := strings.Index(lines[i], phLine)
		if idx < 0 {
			continue
		}
		lines[i] = lines[i][:idx] + dim.Render(phLine) + lines[i][idx+len(phLine):]
	}
	return strings.Join(lines, "\n")
}

// fieldRow assembles one labelled row: leading prefix (▸ for the
// focused field, two spaces otherwise) + padded label + the
// bubbles input's already-rendered View. Multi-line input values
// get the label only on the first row; continuation lines align
// under the input column so a multi-line matchers buffer reads
// as one block visually.
//
// Labels are rendered foreground-only and bold. Body.Default
// carries a background colour for the page chrome — painting it
// behind every label would draw a stripe that doesn't match the
// inputs alongside, so its foreground is extracted explicitly.
// Header.Accent is already foreground-only per the theme spec
// but isn't bold; Bold(true) is a real apply on both branches so
// labels read as row headers regardless of focus state, while
// Header.Accent's yellow singles out the active row.
func (f *Form) fieldRow(label string, idx fieldIndex, rendered string) string {
	prefix := "  "
	labelStyle := lipgloss.NewStyle().
		Foreground(f.styles.Body.Default.GetForeground()).
		Bold(true)
	if idx == f.focus {
		prefix = "▸ "
		labelStyle = f.styles.Header.Accent.Bold(true)
	}
	labelText := labelStyle.Render(format.PadRight(label+":", labelWidth))
	lines := strings.Split(rendered, "\n")
	for i, ln := range lines {
		if i == 0 {
			lines[i] = prefix + labelText + ln
		} else {
			lines[i] = strings.Repeat(" ", 2+labelWidth) + ln
		}
	}
	return strings.Join(lines, "\n")
}
