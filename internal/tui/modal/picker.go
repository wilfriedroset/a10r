// SPDX-License-Identifier: Apache-2.0

package modal

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sahilm/fuzzy"
)

// PickerMode toggles between picking one item (Enter selects) and
// picking many (Space toggles, Enter submits the marked set).
type PickerMode int

const (
	// PickerSingle returns a one-item selection on Enter and
	// ignores Space marks.
	PickerSingle PickerMode = iota
	// PickerMulti accumulates Space-toggled marks and returns the
	// full set on Enter. `a` toggles all currently filtered items.
	PickerMulti
)

// PickerSubmittedMsg is emitted when the user accepts the picker's
// current selection. Selections are returned as the original item
// strings (not indexes) so the caller can treat the picker as
// stateless once the message arrives.
type PickerSubmittedMsg struct {
	Selections []string
}

// IsModalResult satisfies ResultMsg.
func (PickerSubmittedMsg) IsModalResult() {}

// PickerCancelledMsg is emitted on Esc. Carries no selection.
type PickerCancelledMsg struct{}

// IsModalResult satisfies ResultMsg.
func (PickerCancelledMsg) IsModalResult() {}

// Picker is the fuzzy-matched item picker per C3 / k9s audit §3.
// Items are rendered top-down with the cursor highlighted; typing
// narrows the list via fuzzy match, Up/Down (or j/k) walk it.
//
// The picker doesn't know it's the tenant picker — the same shape
// will host receiver / silence picking later. The caller wraps it
// in a thin Modal that knows the title and the message wiring.
type Picker struct {
	title string
	mode  PickerMode

	items   []string
	query   string
	cursor  int
	marks   map[int]struct{} // selected item indexes; multi mode
	matches []int            // filtered item indexes after Find
}

// NewPicker constructs a Picker over the supplied items. The
// initial filtered set is the full input list and the cursor is
// at the top.
func NewPicker(title string, items []string, mode PickerMode) *Picker {
	p := &Picker{
		title: title,
		mode:  mode,
		items: items,
		marks: map[int]struct{}{},
	}
	p.refilter()
	return p
}

// Init implements Modal. The picker has no startup work.
func (*Picker) Init() tea.Cmd { return nil }

// Title implements Modal — the App renders this in the outer panel
// border so the user sees what the picker is for at a glance.
func (p *Picker) Title() string { return p.title }

// Update implements Modal. Returns the same Modal pointer (no
// derivative type) and an optional Cmd that emits the resolution
// message on submit or cancel.
func (p *Picker) Update(msg tea.Msg) (Modal, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	if cmd, terminal := p.handleTerminalKey(keyMsg); terminal {
		return p, cmd
	}
	if p.handleNavOrEdit(keyMsg) {
		return p, nil
	}
	p.handleQueryInput(keyMsg)
	return p, nil
}

// handleTerminalKey processes keys that resolve the modal (Enter,
// Esc). Returns (cmd, true) when the key was terminal so the caller
// can stop walking the keymap.
func (p *Picker) handleTerminalKey(keyMsg tea.KeyMsg) (tea.Cmd, bool) {
	switch keyMsg.String() {
	case "enter":
		cmd := p.submit()
		return cmd, true
	case "esc":
		return func() tea.Msg { return PickerCancelledMsg{} }, true
	}
	return nil, false
}

// handleNavOrEdit processes navigation, mark-toggle, and buffer-
// edit keys. Returns true when the key was handled here.
func (p *Picker) handleNavOrEdit(keyMsg tea.KeyMsg) bool {
	switch keyMsg.String() {
	case "up", "ctrl+p":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "ctrl+n":
		if p.cursor < len(p.matches)-1 {
			p.cursor++
		}
	case "space":
		if p.mode == PickerMulti && len(p.matches) > 0 {
			p.toggleAt(p.matches[p.cursor])
		}
	case "ctrl+u":
		p.query = ""
		p.refilter()
	case "backspace":
		if p.query != "" {
			r := []rune(p.query)
			p.query = string(r[:len(r)-1])
			p.refilter()
		}
	default:
		return false
	}
	return true
}

// handleQueryInput processes printable runes — either select-all
// (multi mode, empty query, `a` key) or appending to the filter
// buffer.
func (p *Picker) handleQueryInput(keyMsg tea.KeyMsg) {
	if p.mode == PickerMulti && keyMsg.String() == "a" && p.query == "" {
		p.selectAllFiltered()
		return
	}
	k := keyMsg.Key()
	if k.Mod != 0 {
		return
	}
	r := k.Text
	if r == "" && k.Code > 0 && unicode.IsPrint(k.Code) {
		r = string(k.Code)
	}
	if r == "" {
		return
	}
	p.query += r
	p.refilter()
}

// submit returns the Cmd that emits a PickerSubmittedMsg with the
// current selection. Single mode picks the cursor row; multi mode
// returns every marked index in original-input order.
func (p *Picker) submit() tea.Cmd {
	var sel []string
	if p.mode == PickerSingle {
		if len(p.matches) == 0 {
			return func() tea.Msg { return PickerCancelledMsg{} }
		}
		sel = []string{p.items[p.matches[p.cursor]]}
	} else {
		sel = make([]string, 0, len(p.marks))
		for i, item := range p.items {
			if _, ok := p.marks[i]; ok {
				sel = append(sel, item)
			}
		}
	}
	return func() tea.Msg { return PickerSubmittedMsg{Selections: sel} }
}

// toggleAt flips a mark in multi mode.
func (p *Picker) toggleAt(idx int) {
	if _, ok := p.marks[idx]; ok {
		delete(p.marks, idx)
		return
	}
	p.marks[idx] = struct{}{}
}

// selectAllFiltered marks every currently visible item. Re-running
// it doesn't unmark — the caller can clear by Ctrl+U then `a`.
func (p *Picker) selectAllFiltered() {
	for _, i := range p.matches {
		p.marks[i] = struct{}{}
	}
}

// refilter rebuilds the matches slice from the current query and
// clamps the cursor to the new range. Called whenever query
// changes.
func (p *Picker) refilter() {
	if p.query == "" {
		p.matches = p.matches[:0]
		for i := range p.items {
			p.matches = append(p.matches, i)
		}
	} else {
		hits := fuzzy.Find(p.query, p.items)
		p.matches = p.matches[:0]
		for _, m := range hits {
			p.matches = append(p.matches, m.Index)
		}
	}
	if p.cursor >= len(p.matches) {
		p.cursor = max(len(p.matches)-1, 0)
	}
}

// View implements Modal. Renders title + query line + filtered
// items with the cursor row highlighted. Marked rows in multi mode
// carry a leading "[x] " marker so the user can spot what they've
// accumulated without leaving the picker.
//
// TODO(theming): the picker currently styles only via lipgloss
// width-fitting; a future commit will take theme.Styles via an
// extended interface so the cursor row, marks, and title pick up
// the active skin's modal colours. Today's plain-text fallback is
// readable on every terminal and reviewable as-is.
func (p *Picker) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(p.title)
	b.WriteString("\n")
	b.WriteString("> " + p.query + "_")
	b.WriteString("\n\n")

	maxRows := height - 4
	for i, idx := range p.matches {
		if i >= maxRows {
			break
		}
		row := p.items[idx]
		marker := "  "
		if p.mode == PickerMulti {
			if _, ok := p.marks[idx]; ok {
				marker = "[x] "
			} else {
				marker = "[ ] "
			}
		}
		line := marker + row
		if i == p.cursor {
			line = "▸ " + line
		} else {
			line = "  " + line
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}
