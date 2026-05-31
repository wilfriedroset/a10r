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
	// PickerSingle returns a one-item selection on Enter and ignores
	// Space marks.
	PickerSingle PickerMode = iota
	// PickerMulti accumulates Space-toggled marks and returns the full set
	// on Enter; `a` toggles all currently filtered items.
	PickerMulti
)

// PickerSubmittedMsg is emitted when the user accepts the picker's
// current selection. Origin is an opaque caller-stamped tag (see
// WithOrigin) the App's lifecycle router uses to distinguish the global
// tenant-quick-switch picker (Origin=="scope") from submissions a focused
// page consumes itself; empty Origin keeps the default behaviour.
type PickerSubmittedMsg struct {
	Origin string
	// Selections carries the selected item strings in original-input order.
	Selections []string
	// Indexes mirrors Selections by original-input index. Callers wrapping
	// the picker over a non-unique label set must use Indexes to
	// disambiguate — Selections collapses identical strings.
	Indexes []int
}

// IsModalResult satisfies ResultMsg.
func (PickerSubmittedMsg) IsModalResult() {}

// PickerCancelledMsg is emitted on Esc. Origin mirrors
// PickerSubmittedMsg.Origin so a cancelled form-side picker reaches the
// form, not the global scope handler.
type PickerCancelledMsg struct {
	Origin string
}

// IsModalResult satisfies ResultMsg.
func (PickerCancelledMsg) IsModalResult() {}

// Picker is the fuzzy-matched item picker (the k9s-style "type to narrow,
// j/k to navigate" affordance). It is content-agnostic — the same shape
// hosts tenant, receiver, or silence picking; the caller wraps it in a
// thin Modal that knows the title and message wiring. origin is the
// opaque tag stamped onto every emitted message (see WithOrigin).
type Picker struct {
	title  string
	mode   PickerMode
	origin string

	items   []string
	query   string
	cursor  int
	marks   map[int]struct{} // selected item indexes; multi mode
	matches []int            // filtered item indexes after Find
}

// NewPicker constructs a Picker over the supplied items, with the full
// input list as the initial filtered set and the cursor at the top.
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

// WithOrigin stamps the picker so every emitted submit/cancel message
// carries the supplied tag, and returns the receiver for chaining. The
// tag is opaque to the picker; the App's router and page Updates agree on
// the namespace (e.g. "scope", "silence-form-tenant").
func (p *Picker) WithOrigin(origin string) *Picker {
	p.origin = origin
	return p
}

// Init implements Modal.
func (*Picker) Init() tea.Cmd { return nil }

// Title implements Modal.
func (p *Picker) Title() string { return p.title }

// Update implements Modal. Returns the same pointer and an optional Cmd
// that emits the resolution message on submit or cancel.
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

// handleTerminalKey processes keys that resolve the modal (Enter, Esc).
// Returns (cmd, true) when the key was terminal.
func (p *Picker) handleTerminalKey(keyMsg tea.KeyMsg) (tea.Cmd, bool) {
	switch keyMsg.String() {
	case "enter":
		cmd := p.submit()
		return cmd, true
	case "esc":
		origin := p.origin
		return func() tea.Msg { return PickerCancelledMsg{Origin: origin} }, true
	}
	return nil, false
}

// handleNavOrEdit processes navigation, mark-toggle, and buffer-edit
// keys. Returns true when the key was handled here.
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

// handleQueryInput processes printable runes — either select-all (multi
// mode, empty query, `a` key) or appending to the filter buffer.
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

// submit returns the Cmd emitting a PickerSubmittedMsg with the current
// selection: single mode picks the cursor row, multi mode returns every
// marked index in original-input order.
func (p *Picker) submit() tea.Cmd {
	origin := p.origin
	var sel []string
	var idx []int
	if p.mode == PickerSingle {
		if len(p.matches) == 0 {
			return func() tea.Msg { return PickerCancelledMsg{Origin: origin} }
		}
		pickedIdx := p.matches[p.cursor]
		sel = []string{p.items[pickedIdx]}
		idx = []int{pickedIdx}
	} else {
		sel = make([]string, 0, len(p.marks))
		idx = make([]int, 0, len(p.marks))
		for i, item := range p.items {
			if _, ok := p.marks[i]; ok {
				sel = append(sel, item)
				idx = append(idx, i)
			}
		}
	}
	return func() tea.Msg { return PickerSubmittedMsg{Origin: origin, Selections: sel, Indexes: idx} }
}

func (p *Picker) toggleAt(idx int) {
	if _, ok := p.marks[idx]; ok {
		delete(p.marks, idx)
		return
	}
	p.marks[idx] = struct{}{}
}

// selectAllFiltered marks every currently visible item; re-running does
// not unmark.
func (p *Picker) selectAllFiltered() {
	for _, i := range p.matches {
		p.marks[i] = struct{}{}
	}
}

// refilter rebuilds the matches slice from the current query and clamps
// the cursor to the new range.
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

// View implements Modal, rendering the query line plus filtered items
// with the cursor row prefixed "▸ " and (multi mode) marks "[x] ". Body
// is plain-text on purpose: the glyphs read on every terminal and the
// frame already inherits panel colours, so a *theme.Styles seam would buy
// nothing the ASCII arrow doesn't already convey without colour.
func (p *Picker) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	// Title is painted by the App's panel border; the body opens with the
	// query line to avoid double-printing the label.
	var b strings.Builder
	b.WriteString("> " + p.query + "_")
	b.WriteString("\n\n")

	maxRows := height - 3
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
