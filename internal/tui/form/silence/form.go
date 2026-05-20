// SPDX-License-Identifier: Apache-2.0

// Package silence renders the silence-creation / -edit form. v0.1
// composes bubbles' textinput / textarea models for the per-field
// chrome and keeps a thin wrapper for cross-field navigation,
// validation, and the CreateSilence / UpdateSilence verb selection.
//
// Submit calls a backend.Writer via the injected Client interface.
// Success emits SubmittedMsg (auto-pop); failure flashes the error
// and stays on the form so the user can correct and re-submit.
//
// In-package layout (see ADR-0025): form.go owns the Form struct,
// New, and the page-method contract; state.go does parsing /
// validation; fields.go owns the focus state machine; submit.go
// owns the async write protocol; tenant.go the picker; render.go
// the View body.
package silence

import (
	"context"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
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

// Form is the silence-creation / silence-edit page. Implements
// app.Page. Mode is selected by editID: empty → CreateSilence on
// submit; non-empty → UpdateSilence(editID, spec) on submit.
type Form struct {
	// clients is the writeable backend map keyed by tenant. Submit
	// routes to clients[tenant]; the Tenant row's Enter opens a
	// picker over the keys. Per ADR-0011 the form takes the full
	// map so the user — not the caller — picks the write target.
	clients map[string]Client
	// tenant is the currently-selected tenant name. Mutated only by
	// a PickerSubmittedMsg landing on the form; empty in bulk mode.
	tenant string
	styles *theme.Styles
	now    func() time.Time

	// matchers is the multi-line buffer holding one matcher per
	// line. Bubbles' textarea handles cursor / wrap / paste for free.
	matchers textarea.Model
	// Single-line scalar fields backed by bubbles' textinput.
	starts  textinput.Model
	ends    textinput.Model
	creator textinput.Model
	comment textinput.Model

	// editID, when non-empty, switches submit to UpdateSilence and
	// the title to "edit silence <id>".
	editID string

	// bulk hides matchers, skips matcher validation, renders the
	// banner instead of the textarea, and routes submit through
	// BulkSubmittedMsg.
	bulk bool
	// bulkBanner renders in place of the matchers buffer when bulk
	// is true. See Options.BulkBanner.
	bulkBanner string

	focus fieldIndex
	err   string // last submit error; cleared on next keystroke

	// submit owns the async write lifecycle (see submit.go).
	submit submitter
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
	// through matcher.Parse so editing via Tab + backspace works
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
	// is mutually exclusive with EditID — bulk-edit is intentionally
	// out of scope (editing N silences in lockstep has no obvious UX
	// and no current ask).
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
		submit:     submitter{parent: opts.SubmitCtx},
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

func (*Form) Init() tea.Cmd { return nil }

// Close implements app.Page. Cancels any in-flight write so a
// slow tenant doesn't keep writing after the user pops the form:
// without this, Esc-then-page-swap leaves the goroutine running
// and the silence is created/updated on the server with no
// operator confirmation in the TUI.
func (f *Form) Close() tea.Cmd {
	f.submit.Cancel()
	return nil
}

// CapturesInput implements app.InputCapturePage so the form
// receives `q`, `:`, `/`, `?`, `0`-`9` as text. Esc still cancels
// via the form's own handler.
func (*Form) CapturesInput() bool { return true }

func (*Form) Crumb() string { return "silence" }

// Title implements app.Page. "new silence" / "edit silence <id>"
// / "bulk silence" so the user always knows which verb submit will
// fire. The bulk form's banner carries the per-target breakdown.
func (f *Form) Title() string {
	if f.bulk {
		return "bulk silence"
	}
	if f.editID != "" {
		return "edit silence " + f.editID
	}
	return "new silence"
}

// HeaderContent implements app.Page. Empty — Title() already
// labels the panel border, a subtitle echo would duplicate it.
func (*Form) HeaderContent() string { return "" }

// Footer implements app.Page. Form doesn't surface ambient state.
func (*Form) Footer() string { return "" }

func (*Form) Bindings() []action.Action {
	return []action.Action{
		{Key: "Tab", Description: "next field", View: "silence-form"},
		{Key: "Shift+Tab", Description: "prev field", View: "silence-form"},
		{Key: "Enter", Description: "pick tenant (on Tenant row)", View: "silence-form"},
		{Key: "Ctrl+S", Description: "submit", View: "silence-form"},
	}
}

// Update implements app.Page. Cross-field navigation and submit
// land here directly; every other message forwards to the focused
// bubbles input. The non-key forward is load-bearing: cursor blink
// is driven by cursor.BlinkMsg, not by KeyPressMsg, and dropping
// non-key messages would silence the blink loop.
func (f *Form) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if m, ok := msg.(submitDoneMsg); ok {
		cmd := f.applySubmitDone(m)
		return f, cmd
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
			cmd := f.submitNow()
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

// View implements app.Page. Delegates to renderView (render.go).
func (f *Form) View(width, height int) string { return f.renderView(width, height) }
