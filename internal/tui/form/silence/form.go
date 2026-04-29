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
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Client is the small write surface the form needs. Allows tests
// to inject a fake without booting the real backend wiring. Both
// CreateSilence and UpdateSilence are listed because the form can
// land in either mode (Options.EditID empty / non-empty); a single
// interface keeps the wiring layer free of two parallel adapters.
type Client interface {
	CreateSilence(ctx context.Context, spec backend.SilenceSpec) (string, error)
	UpdateSilence(ctx context.Context, id string, spec backend.SilenceSpec) error
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

// fieldIndex enumerates the form's input slots so Tab navigation
// can walk them in display order.
type fieldIndex int

const (
	fieldMatchers fieldIndex = iota
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
	client Client
	styles theme.Styles
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

	focus fieldIndex
	err   string // last submit error; cleared on next keystroke
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
	Client Client
	Styles theme.Styles
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
	if len(opts.Matchers) > 0 {
		matchers.SetValue(formatMatchers(opts.Matchers))
	}

	starts := newInput("now")
	ends := newInput("2h")
	if !opts.EndsAt.IsZero() {
		ends.SetValue(opts.EndsAt.UTC().Format(time.RFC3339))
	} else {
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
		client:   opts.Client,
		styles:   opts.Styles,
		now:      now,
		matchers: matchers,
		starts:   starts,
		ends:     ends,
		creator:  creator,
		comment:  comment,
		editID:   opts.EditID,
	}
	// Focus the first field so the user lands ready to type.
	_ = f.activeFocus()
	return f
}

// newInput constructs a textinput.Model with the form's shared
// shape: no built-in prompt (the row label provides one), the
// supplied placeholder for empty state, no character limit, and
// flattened text + placeholder styling so every input renders
// in the body's default foreground regardless of focus or
// fill state. The form's only focus indicator is the leading
// `▸` marker plus the row label's accent tint — bubbles' default
// dim-grey placeholder + dim-grey blurred text would compete
// with that and make every blurred / unfilled field look stale.
func newInput(placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	s := in.Styles()
	s.Blurred.Text = s.Focused.Text
	s.Focused.Placeholder = s.Focused.Text
	s.Blurred.Placeholder = s.Focused.Text
	in.SetStyles(s)
	return in
}

// flattenTextareaBlur strips dim and background highlights from
// the textarea so matchers reads identically — same fg, no bg
// stripe — whether focused or blurred. Bubbles' defaults paint
// a CursorLine background and dim blurred text + placeholder;
// the form's focus marker is the leading `▸` + accent label,
// not a rectangular highlight that competes with the row chrome.
func flattenTextareaBlur(m *textarea.Model) {
	s := m.Styles()
	bare := lipgloss.NewStyle()
	s.Focused.Text = bare
	s.Blurred.Text = bare
	s.Focused.Placeholder = bare
	s.Blurred.Placeholder = bare
	s.Focused.CursorLine = bare
	s.Blurred.CursorLine = bare
	m.SetStyles(s)
}

// Init implements app.Page.
func (*Form) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Form) Close() tea.Cmd { return nil }

// CapturesInput implements app.InputCapturePage so the form
// receives `q`, `:`, `/`, `?`, `0`-`9` as text input rather than
// having the dispatcher consume them. Esc still cancels via the
// form's own handler.
func (*Form) CapturesInput() bool { return true }

// Crumb implements app.Page.
func (*Form) Crumb() string { return "silence" }

// Title implements app.Page. Reads "new silence" in create mode
// and "edit silence <id>" in edit mode so the user always knows
// which verb the submit will fire.
func (f *Form) Title() string {
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

// Bindings implements app.Page.
func (*Form) Bindings() []action.Action {
	return []action.Action{
		{Key: "Tab", Description: "next field", View: "silence-form"},
		{Key: "Shift+Tab", Description: "prev field", View: "silence-form"},
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
		}
	}
	cmd := f.forwardToFocused(msg)
	return f, cmd
}

// forwardToFocused dispatches the message to whichever bubbles
// input is currently focused. Each model's Update returns a
// fresh copy (value receiver), so the slot is reassigned in
// place. Accepts tea.Msg (not just KeyPressMsg) so cursor blink
// ticks and paste completions reach the focused field too.
func (f *Form) forwardToFocused(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch f.focus {
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
func (f *Form) cycleFocus(delta int) tea.Cmd {
	f.activeBlur()
	f.focus = (f.focus + fieldIndex(delta) + numFields) % numFields
	return f.activeFocus()
}

// activeFocus calls Focus on the field at the current index.
func (f *Form) activeFocus() tea.Cmd {
	switch f.focus {
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

// submit parses the buffers and either creates or updates the
// silence depending on whether the form is in edit mode. On
// success emits SubmittedMsg carrying the relevant ID; on failure
// surfaces the error as a flash and stays on the form.
func (f *Form) submit() tea.Cmd {
	if f.client == nil {
		return f.fail("client not configured")
	}
	spec, err := f.parseSpec()
	if err != nil {
		return f.fail(err.Error())
	}
	if f.editID != "" {
		if err := f.client.UpdateSilence(context.Background(), f.editID, spec); err != nil {
			return f.fail(err.Error())
		}
		id := f.editID
		return func() tea.Msg { return SubmittedMsg{ID: id, Updated: true} }
	}
	id, err := f.client.CreateSilence(context.Background(), spec)
	if err != nil {
		return f.fail(err.Error())
	}
	return func() tea.Msg { return SubmittedMsg{ID: id} }
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
// Returns the first validation error encountered.
func (f *Form) parseSpec() (backend.SilenceSpec, error) {
	matchers, err := parseMatchers(f.matchers.Value())
	if err != nil {
		return backend.SilenceSpec{}, err
	}
	if len(matchers) == 0 {
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
// relative to base, or an RFC3339 timestamp. Empty falls back to
// base + 2h.
func parseEndsAt(in string, base time.Time) (time.Time, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return base.Add(2 * time.Hour), nil
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

	rows := []string{
		f.fieldRow("Matchers", fieldMatchers, f.matchers.View()),
		f.fieldRow("Starts", fieldStarts, f.starts.View()),
		f.fieldRow("Ends", fieldEnds, f.ends.View()),
		f.fieldRow("Creator", fieldCreator, f.creator.View()),
		f.fieldRow("Comment", fieldComment, f.comment.View()),
	}
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
	labelText := labelStyle.Render(padRight(label+":", labelWidth))
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

// padRight pads s with spaces to exactly w columns. Used for the
// label column so every input lines up regardless of label
// length.
func padRight(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}
