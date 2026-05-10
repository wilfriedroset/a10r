// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/panel"
)

// View implements tea.Model. Composes the k9s-style top panel,
// a bordered body containing either the open modal or the top
// page's view, and the footer (crumbs + prompt + flash) into a
// full-screen alt-screen view.
func (a *App) View() tea.View {
	if a.width == 0 || a.height == 0 {
		// Pre-resize: bubbletea's first WindowSizeMsg arrives in
		// the next loop iteration. Render an empty alt-screen
		// view so we don't crash with a zero-width Render.
		v := tea.NewView("")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	topPanel := panel.RenderTop(a.panelState(), a.styles)
	promptPanel := a.renderPromptPanel()
	footerLines := a.renderFooter()

	bodyHeight := max(
		a.height-linesIn(topPanel)-linesIn(promptPanel)-linesIn(footerLines),
		0,
	)
	body := a.renderBody(bodyHeight)

	parts := []string{topPanel}
	if promptPanel != "" {
		parts = append(parts, promptPanel)
	}
	parts = append(parts, body)
	if footerLines != "" {
		parts = append(parts, footerLines)
	}
	out := lipgloss.JoinVertical(lipgloss.Left, parts...)
	v := tea.NewView(out)
	v.AltScreen = true
	// Cell-motion mouse mode lets the terminal forward wheel ticks
	// (and click/release/motion) into the program. The app routes
	// wheel events to cursor walk on tables and the help modal's
	// scroll offset; click and motion are explicitly ignored
	// (keyboard-first contract — no click-to-focus, no drag-select).
	// All-motion mode would add hover events the app has no use for
	// and a chunk of terminal traffic we don't need.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// renderPromptPanel returns the bordered prompt panel that sits
// directly above the body when `:` or `/` is open. Empty otherwise
// so the body fills the freed rows. K9s mirror — the prompt is
// part of the chrome above the resource view, not a footer strip.
func (a *App) renderPromptPanel() string {
	if !a.prompt.IsOpen() {
		return ""
	}
	body := a.prompt.Render(a.styles)
	return panel.RenderFrame(a.width, body, a.styles)
}

// panelState builds the top-panel state from the app shell's
// view of the world plus the active page's metadata. Info column
// shows tenant + version; tenants column shows the numeric
// shortcuts; hints column comes from the top page's Bindings;
// logo is the package-level constant.
func (a *App) panelState() panel.State {
	state := panel.State{
		Width:   a.width,
		Logo:    panel.Logo,
		Tenants: tenantBindings(a.tenants),
		Info: []panel.InfoLine{
			{Label: "tenants", Value: "—"},
			{Label: "alerts", Value: "—"},
			{Label: "version", Value: "v0.1.0"},
		},
	}
	if p := a.topPage(); p != nil {
		state.Hints = p.Bindings()
	}
	return state
}

// tenantBindings produces the numeric shortcut catalogue for the
// panel: <0> all + <1>..<9> for the first nine configured
// tenants. Empty input still emits <0> all so the column is
// present-but-trivial in zero-backend / wizard runs.
func tenantBindings(tenants []string) []panel.TenantBinding {
	out := make([]panel.TenantBinding, 0, len(tenants)+1)
	out = append(out, panel.TenantBinding{Key: "0", Name: "all"})
	for i, t := range tenants {
		if i >= 9 {
			break // numeric quick-switch tops out at 1-9 per C3
		}
		out = append(out, panel.TenantBinding{Key: strconv.Itoa(i + 1), Name: t})
	}
	return out
}

// renderBody fills the bordered-body slot. Modal wins when one
// is open; the top page draws its View otherwise; an empty stack
// renders a styled blank pane. When the active page has a non-
// empty HeaderContent (filter / sort / mark indicators), it
// renders as a subtitle line directly below the title border so
// the user can spot the active shaping at a glance. Page Footer
// (e.g. silences's "next refresh 26s") rides the bottom border —
// modals don't get one, the bottom edge stays a plain rule for
// them.
func (a *App) renderBody(height int) string {
	innerHeight := max(height-2, 0) // -2 for top + bottom borders
	innerWidth := max(a.width-2, 0)

	var inner, title, pageFooter string
	switch {
	case a.modal != nil:
		title = a.modal.Title()
		if title == "" {
			title = "modal"
		}
		inner = a.modal.View(innerWidth, innerHeight)
	case a.topPage() != nil:
		p := a.topPage()
		title = p.Title()
		if title == "" {
			title = p.Crumb()
		}
		// When the filter prompt is open, surface the live-typed
		// value in the title's `</value>` segment so the user can
		// see the active filter without leaving the body in their
		// peripheral vision. Mirrors the k9s "/-prompt visible"
		// affordance. Closed prompt OR command mode → no append.
		if a.prompt.IsOpen() && a.prompt.Mode() == footer.PromptFilter {
			title += " </" + a.prompt.Value() + ">"
		}
		subtitle := p.HeaderContent()
		if subtitle != "" {
			inner = subtitle + "\n" + p.View(innerWidth, max(innerHeight-1, 0))
		} else {
			inner = p.View(innerWidth, innerHeight)
		}
		pageFooter = p.Footer()
	default:
		title = "a10r"
		inner = ""
	}
	return panel.RenderBody(a.width, height, inner, title, pageFooter, a.styles)
}

// renderFooter stacks the hint / crumbs / flash strips. The prompt
// has moved up above the body (renderPromptPanel) — when open it is
// part of the chrome, not a footer line. Each strip can be empty;
// the join collapses empty rows so the body fills the freed space.
//
// Hint-bar order: the rotating tip strip (P2.W1.7) sits above the
// crumbs so the breadcrumb line stays the closest cue to the body
// — the user reads the page stack first, the curated tip second.
// A disabled hint bar renders empty and the row collapses.
func (a *App) renderFooter() string {
	parts := make([]string, 0, 3)
	if s := a.hintbar.Render(a.styles); s != "" {
		parts = append(parts, s)
	}
	if s := a.crumbs.Render(a.styles); s != "" {
		parts = append(parts, s)
	}
	if s := a.flash.Render(a.styles); s != "" {
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// linesIn returns the number of "\n"-separated rows in s. Empty
// string is zero rows (collapses) so callers can treat an empty
// strip the same as no strip at all.
func linesIn(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
