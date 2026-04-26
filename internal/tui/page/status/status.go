// SPDX-License-Identifier: Apache-2.0

// Package status renders the alertmanager status pane: cluster
// state, version info, uptime, and the raw `config.original` YAML
// per I1. Three anchor keys (c/p/v) scroll to the cluster /
// version / config sections respectively.
package status

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Page renders the AM /status output.
type Page struct {
	styles theme.Styles
	tenant string

	have   bool
	st     backend.Status
	scroll int // first visible line index
}

// New constructs an empty status page.
func New(styles theme.Styles, tenant string) *Page {
	return &Page{styles: styles, tenant: tenant}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "status" }

// Title implements app.Page.
func (p *Page) Title() string {
	if p.tenant == "" {
		return "status"
	}
	return "status(" + p.tenant + ")"
}

// HeaderContent implements app.Page.
func (p *Page) HeaderContent() string {
	if !p.have {
		return "status: (loading)"
	}
	return fmt.Sprintf("%s · v%s · uptime %s", p.tenant, p.st.Version.Version, p.st.Uptime)
}

// Bindings implements app.Page.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "c", Description: "cluster", View: "status"},
		{Key: "v", Description: "version", View: "status"},
		{Key: "p", Description: "config", View: "status"},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case poll.DataMsg:
		s, ok := m.Resource.(backend.Status)
		if !ok {
			return p, nil
		}
		p.st = s
		p.have = true
		return p, nil
	case app.GoToFirstRowMsg:
		// `gg` scrolls back to the top of the status document.
		// The single-`g` handler is intentionally absent — once
		// the dispatcher's chord buffer is active globally, the
		// first `g` is consumed before this Update runs anyway.
		p.scroll = 0
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m), nil
	}
	return p, nil
}

func (p *Page) handleKey(m tea.KeyPressMsg) app.Page {
	lines := p.lines()
	switch m.String() {
	case "j", "down":
		if p.scroll < len(lines)-1 {
			p.scroll++
		}
	case "k", "up":
		if p.scroll > 0 {
			p.scroll--
		}
	case "ctrl+d":
		p.scroll = min(p.scroll+10, max(len(lines)-1, 0))
	case "ctrl+u":
		p.scroll = max(p.scroll-10, 0)
	// `g` alone is dead code — the dispatcher's chord buffer at
	// LayerTable consumes the first `g` waiting for the second.
	// The chord-completed `gg` arrives as app.GoToFirstRowMsg and
	// is handled in Update.
	case "G":
		p.scroll = max(len(lines)-1, 0)
	case "c":
		p.scroll = p.anchorCluster(lines)
	case "v":
		p.scroll = p.anchorVersion(lines)
	case "p":
		p.scroll = p.anchorConfig(lines)
	}
	return p
}

// View implements app.Page. Renders the linearised status output
// starting at p.scroll, capped at the available height.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	all := p.lines()
	if len(all) == 0 {
		return p.styles.Body.Default.Width(width).Height(height).Render("status: (no data)")
	}
	end := min(p.scroll+height, len(all))
	visible := all[p.scroll:end]
	return lipgloss.NewStyle().Width(width).Render(strings.Join(visible, "\n"))
}

// lines returns the rendered status as a flat line slice. The
// helpers anchorCluster / anchorVersion / anchorConfig know the
// section boundaries by scanning for the section headers.
func (p *Page) lines() []string {
	if !p.have {
		return nil
	}
	peerNames := make([]string, len(p.st.Cluster.Peers))
	for i, peer := range p.st.Cluster.Peers {
		peerNames[i] = peer.Name
	}
	configLines := strings.Split(p.st.Config, "\n")
	out := make([]string, 0, 14+len(configLines))
	out = append(out,
		"== Cluster ==",
		"  status: "+p.st.Cluster.Status,
		"  peers:  "+strings.Join(peerNames, ", "),
		"",
		"== Version ==",
		"  version:    "+p.st.Version.Version,
		"  revision:   "+p.st.Version.Revision,
		"  branch:     "+p.st.Version.Branch,
		"  build user: "+p.st.Version.BuildUser,
		"  build date: "+p.st.Version.BuildDate,
		"  go version: "+p.st.Version.GoVersion,
		"",
		"== Config ==",
	)
	out = append(out, configLines...)
	return out
}

func (p *Page) anchorCluster(lines []string) int { return findHeader(lines, "== Cluster ==") }
func (p *Page) anchorVersion(lines []string) int { return findHeader(lines, "== Version ==") }
func (p *Page) anchorConfig(lines []string) int  { return findHeader(lines, "== Config ==") }

// findHeader returns the index of the first line equal to header,
// or 0 if the header isn't present (so the anchor key never
// scrolls past the document).
func findHeader(lines []string, header string) int {
	for i, l := range lines {
		if l == header {
			return i
		}
	}
	return 0
}
