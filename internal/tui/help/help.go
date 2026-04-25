// SPDX-License-Identifier: Apache-2.0

// Package help renders the help overlay opened by `?`. It groups
// the active bindings into globals / per-view / table-context
// sections and hides Dangerous entries under read-only mode (C4).
// Auto-built from the action registry so adding a binding
// elsewhere automatically surfaces in the overlay without
// touching this file.
package help

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// Options bundles the inputs the overlay needs.
type Options struct {
	Registry *action.Registry
	View     string
	ReadOnly bool
}

// Help is the modal overlay.
type Help struct {
	registry *action.Registry
	view     string
	readOnly bool
}

// New constructs a Help modal.
func New(opts Options) *Help {
	return &Help{
		registry: opts.Registry,
		view:     opts.View,
		readOnly: opts.ReadOnly,
	}
}

// Init implements modal.Modal.
func (*Help) Init() tea.Cmd { return nil }

// Update implements modal.Modal. Any keystroke dismisses the
// overlay — it's read-only so there's no other useful action.
func (h *Help) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return h, func() tea.Msg { return modal.HelpClosedMsg{} }
	}
	return h, nil
}

// View implements modal.Modal.
func (h *Help) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if h.registry == nil {
		return lipgloss.NewStyle().Width(width).Render("help: action registry not configured")
	}
	all := h.registry.Filter(h.readOnly)
	globals, viewBound, tableBound := bucketize(all, h.view)
	sections := []string{
		"== Help ==",
		"  any key dismisses",
		"",
		section("Global", globals),
		"",
		section("View — "+h.viewLabel(), viewBound),
		"",
		section("Table", tableBound),
	}
	body := strings.Join(sections, "\n")
	return lipgloss.NewStyle().Width(width).Render(body)
}

// viewLabel returns a friendly label for the active view, or
// "(none)" when no view is set.
func (h *Help) viewLabel() string {
	if h.view == "" {
		return "(none)"
	}
	return h.view
}

// bucketize splits actions into globals (View == ""), per-view
// (View == h.view), and table-context (View == "table"). Anything
// else falls into the per-view bucket so user-supplied view names
// don't disappear; in v0.1 only "table" is treated as a special
// shared layer.
func bucketize(actions []action.Action, view string) (globals, viewBound, tableBound []action.Action) {
	for _, a := range actions {
		switch a.View {
		case "":
			globals = append(globals, a)
		case "table":
			tableBound = append(tableBound, a)
		case view:
			viewBound = append(viewBound, a)
		}
	}
	sort.Slice(globals, func(i, j int) bool { return globals[i].Key < globals[j].Key })
	sort.Slice(viewBound, func(i, j int) bool { return viewBound[i].Key < viewBound[j].Key })
	sort.Slice(tableBound, func(i, j int) bool { return tableBound[i].Key < tableBound[j].Key })
	return globals, viewBound, tableBound
}

// section renders one heading + its bindings. Empty input renders
// as "(none)".
func section(title string, actions []action.Action) string {
	var b strings.Builder
	b.WriteString(title + ":\n")
	if len(actions) == 0 {
		b.WriteString("  (none)")
		return b.String()
	}
	for i, a := range actions {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  [" + a.Key + "]  " + a.Description)
	}
	return b.String()
}
