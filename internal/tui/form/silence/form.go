// SPDX-License-Identifier: Apache-2.0

// Package silence renders the silence-creation form. v0.1 ships a
// hand-rolled, keyboard-driven form (no huh dependency) — five
// scalar fields plus a free-form Matchers field encoded as
// "name=value" / "name=~regex" lines. Enough to land a silence;
// the form's affordances are deliberately small so the surface
// area stays reviewable in one commit.
//
// Submit calls a backend.Writer.CreateSilence via the injected
// Client interface. Success pops the form (caller responsibility:
// the SubmittedMsg the form emits drives a popPage). Errors stay
// in the form (the user fixes and re-submits) and surface as a
// flash.
package silence

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Client is the small write surface the form needs. Allows tests
// to inject a fake without booting the real backend wiring.
type Client interface {
	CreateSilence(ctx context.Context, spec backend.SilenceSpec) (string, error)
}

// SubmittedMsg is emitted on a successful submit. ID carries the
// silence ID returned by the backend so the caller (typically
// the silences list page) can hint, navigate, etc.
type SubmittedMsg struct {
	ID string
}

// CancelledMsg is emitted on Esc with no submission.
type CancelledMsg struct{}

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

// Form is the silence-creation page. Implements app.Page.
type Form struct {
	client Client
	styles theme.Styles
	now    func() time.Time

	// Field buffers. Strings throughout — we parse on submit so
	// validation errors all surface at once with line-precise hints.
	matchers string // free-form: one matcher per line, "name=value" / "name=~regex"
	starts   string // RFC3339 or "" for "now"
	ends     string // RFC3339 or duration like "2h" / "30m"
	creator  string
	comment  string

	focus fieldIndex
	err   string // last submit error; cleared on next keystroke
}

// Options captures the dependency surface.
type Options struct {
	Client Client
	Styles theme.Styles
	// Now injects the clock used to default StartsAt and resolve
	// duration shorthands like "2h". nil falls back to time.Now.
	Now func() time.Time
	// Creator is the default value for the creator field —
	// typically $USER.
	Creator string
}

// New constructs a Form.
func New(opts Options) *Form {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Form{
		client:  opts.Client,
		styles:  opts.Styles,
		now:     now,
		creator: opts.Creator,
		ends:    "2h",
	}
}

// Init implements app.Page.
func (*Form) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Form) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Form) Crumb() string { return "silence" }

// HeaderContent implements app.Page.
func (*Form) HeaderContent() string { return "new silence" }

// Bindings implements app.Page.
func (*Form) Bindings() []action.Action {
	return []action.Action{
		{Key: "Tab", Description: "next field", View: "silence-form"},
		{Key: "Shift+Tab", Description: "prev field", View: "silence-form"},
		{Key: "Ctrl+S", Description: "submit", View: "silence-form"},
		{Key: "?", Description: "help", View: ""},
	}
}

// Update implements app.Page.
func (f *Form) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, nil
	}
	f.err = ""
	switch keyMsg.String() {
	case "tab":
		f.focus = (f.focus + 1) % numFields
	case "shift+tab":
		f.focus = (f.focus + numFields - 1) % numFields
	case "esc":
		return f, func() tea.Msg { return CancelledMsg{} }
	case "ctrl+s":
		cmd := f.submit()
		return f, cmd
	case "backspace":
		f.backspace()
	case "ctrl+u":
		*f.fieldRef() = ""
	case "enter":
		// Enter inserts a newline only in the matchers field —
		// every other field is single-line and ignores it.
		if f.focus == fieldMatchers {
			*f.fieldRef() += "\n"
		}
	default:
		f.appendInput(keyMsg)
	}
	return f, nil
}

// fieldRef returns a pointer to the focused buffer.
func (f *Form) fieldRef() *string {
	switch f.focus {
	case fieldMatchers:
		return &f.matchers
	case fieldStarts:
		return &f.starts
	case fieldEnds:
		return &f.ends
	case fieldCreator:
		return &f.creator
	case fieldComment:
		return &f.comment
	case numFields:
		// Sentinel — never the active focus. Falls through to
		// the default arm below for safety.
	}
	return &f.comment
}

// appendInput appends the keystroke's printable rune to the focused
// buffer. Falls back to KeyPressMsg.Code when Text is empty (some
// terminals don't populate Text).
func (f *Form) appendInput(km tea.KeyMsg) {
	k := km.Key()
	if k.Mod != 0 {
		return
	}
	r := k.Text
	if r == "" && k.Code >= 0x20 && k.Code != 0x7f {
		r = string(k.Code)
	}
	if r == "" {
		return
	}
	*f.fieldRef() += r
}

// backspace pops one rune from the focused buffer.
func (f *Form) backspace() {
	cur := *f.fieldRef()
	if cur == "" {
		return
	}
	r := []rune(cur)
	*f.fieldRef() = string(r[:len(r)-1])
}

// submit parses the buffers, calls Client.CreateSilence, and
// emits SubmittedMsg on success / a flash on failure.
func (f *Form) submit() tea.Cmd {
	if f.client == nil {
		return f.fail("client not configured")
	}
	spec, err := f.parseSpec()
	if err != nil {
		return f.fail(err.Error())
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
	matchers, err := parseMatchers(f.matchers)
	if err != nil {
		return backend.SilenceSpec{}, err
	}
	if len(matchers) == 0 {
		return backend.SilenceSpec{}, errors.New("at least one matcher is required")
	}
	starts, err := parseTimeOrNow(f.starts, f.now())
	if err != nil {
		return backend.SilenceSpec{}, errors.New("starts: " + err.Error())
	}
	ends, err := parseEndsAt(f.ends, starts)
	if err != nil {
		return backend.SilenceSpec{}, errors.New("ends: " + err.Error())
	}
	if !ends.After(starts) {
		return backend.SilenceSpec{}, errors.New("ends must be after starts")
	}
	if strings.TrimSpace(f.creator) == "" {
		return backend.SilenceSpec{}, errors.New("creator is required")
	}
	if strings.TrimSpace(f.comment) == "" {
		return backend.SilenceSpec{}, errors.New("comment is required")
	}
	return backend.SilenceSpec{
		Matchers:  matchers,
		StartsAt:  starts,
		EndsAt:    ends,
		CreatedBy: strings.TrimSpace(f.creator),
		Comment:   strings.TrimSpace(f.comment),
	}, nil
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

// parseOneMatcher splits a single matcher line on the first
// operator it finds. Order matters — `!~` must be tried before
// `!=` and `~`, etc.
func parseOneMatcher(line string) (backend.Matcher, error) {
	for _, op := range []struct {
		s       string
		isRegex bool
		isEqual bool
	}{
		{s: "!~", isRegex: true, isEqual: false},
		{s: "=~", isRegex: true, isEqual: true},
		{s: "!=", isRegex: false, isEqual: false},
		{s: "=", isRegex: false, isEqual: true},
	} {
		idx := strings.Index(line, op.s)
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+len(op.s):])
		if name == "" || value == "" {
			return backend.Matcher{}, errors.New("matcher must be name<op>value")
		}
		return backend.Matcher{
			Name: name, Value: value,
			IsRegex: op.isRegex, IsEqual: op.isEqual,
		}, nil
	}
	return backend.Matcher{}, errors.New("missing operator (=, !=, =~, !~)")
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

// View implements app.Page. Renders one field per row with the
// focused field marked by a leading "▸". The matchers field shows
// every line of its buffer.
func (f *Form) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	rows := []string{
		f.fieldRow("Matchers", fieldMatchers, f.matchers),
		f.fieldRow("Starts", fieldStarts, placeholder(f.starts, "now")),
		f.fieldRow("Ends", fieldEnds, placeholder(f.ends, "2h")),
		f.fieldRow("Creator", fieldCreator, f.creator),
		f.fieldRow("Comment", fieldComment, f.comment),
	}
	footerLine := "Tab=next  Shift+Tab=prev  Ctrl+S=submit  Esc=cancel"
	if f.err != "" {
		footerLine = "ERR: " + f.err + "    " + footerLine
	}
	body := strings.Join(rows, "\n") + "\n\n" + footerLine
	return lipgloss.NewStyle().Width(width).Render(body)
}

func (f *Form) fieldRow(label string, idx fieldIndex, value string) string {
	prefix := "  "
	if idx == f.focus {
		prefix = "▸ "
	}
	return prefix + label + ": " + value
}

func placeholder(value, ph string) string {
	if value == "" {
		return "(" + ph + ")"
	}
	return value
}
