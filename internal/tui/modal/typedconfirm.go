// SPDX-License-Identifier: Apache-2.0

package modal

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// TypedConfirm is the confirmation a `confirmation: type-tenant-name`
// guardrail demands: the user retypes the backend name before the write
// lands. It resolves through ConfirmResultMsg so a caller handles one
// result type whatever level the rule asked for.
//
// A run that spans several restricted tenants asks for each in turn,
// and one Esc cancels the whole run before any write starts. Stepping
// inside one modal keeps that rule in one place; chaining a modal per
// tenant would leave the App to unwind a half-confirmed run.
type TypedConfirm struct {
	question string
	tenants  []string
	at       int
	typed    string
	mismatch bool
}

// NewGuardedConfirm picks the confirmation a write gets. typed names
// the target tenants a guardrail rule restricts to the typed level, so
// an empty list leaves the verb the yes/no prompt it has today: a rule
// can only strengthen a confirmation, never weaken it.
func NewGuardedConfirm(question string, def ConfirmDefault, typed []string) Modal {
	if len(typed) > 0 {
		return NewTypedConfirm(question, typed)
	}
	return NewConfirm(question, def)
}

// NewTypedConfirm builds the prompt for one write over the given
// restricted tenants, in the order they are asked.
func NewTypedConfirm(question string, tenants []string) *TypedConfirm {
	return &TypedConfirm{question: question, tenants: tenants}
}

func (*TypedConfirm) Init() tea.Cmd { return nil }

// Title implements Modal. It matches Confirm's title so the two
// confirmation levels read as one family in the panel border.
func (*TypedConfirm) Title() string { return "confirm" }

// Update implements Modal. Enter accepts only an exact match on the
// current tenant: a mismatch keeps the prompt open and says what to
// type, because a typo here must never be read as consent.
func (t *TypedConfirm) Update(msg tea.Msg) (Modal, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return t, nil
	}
	switch keyMsg.String() {
	case keyEsc:
		return t, cancel
	case keyEnter:
		return t.accept()
	case keyClearLine:
		t.edit("")
		return t, nil
	case keyBackspace:
		if t.typed != "" {
			r := []rune(t.typed)
			t.edit(string(r[:len(r)-1]))
		}
		return t, nil
	}
	if r := printableRune(keyMsg); r != "" {
		t.edit(t.typed + r)
	}
	return t, nil
}

// edit replaces the typed buffer and drops any standing mismatch, so
// the correction the user just started does not read as still wrong.
func (t *TypedConfirm) edit(typed string) {
	t.typed = typed
	t.mismatch = false
}

// accept advances to the next restricted tenant, or resolves the modal
// once the last one matched. An empty tenant list cancels rather than
// confirms: a guardrail modal with nothing to ask for must fail closed.
func (t *TypedConfirm) accept() (Modal, tea.Cmd) {
	if len(t.tenants) == 0 {
		return t, cancel
	}
	if t.typed != t.tenants[t.at] {
		t.mismatch = true
		return t, nil
	}
	if t.at == len(t.tenants)-1 {
		return t, func() tea.Msg { return ConfirmResultMsg{Yes: true} }
	}
	t.at++
	t.edit("")
	return t, nil
}

func (t *TypedConfirm) want() string {
	if len(t.tenants) == 0 {
		return ""
	}
	return t.tenants[t.at]
}

func cancel() tea.Msg { return ConfirmResultMsg{Cancelled: true} }

func (t *TypedConfirm) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	want := t.want()
	lines := []string{t.question, "", `type "` + want + `" to confirm`, "> " + t.typed}
	if t.mismatch {
		lines = append(lines, "", "that is not "+want)
	}
	escHint := "Esc=cancel"
	if total := len(t.tenants); total > 1 {
		// The position tells the user how far in they are, so an Esc at
		// tenant two is a deliberate choice rather than a surprise.
		lines = append(lines, "", "tenant "+strconv.Itoa(t.at+1)+" of "+strconv.Itoa(total))
		escHint = "Esc=cancel the whole run"
	}
	lines = append(lines, "", "Enter=confirm   "+escHint)
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}
