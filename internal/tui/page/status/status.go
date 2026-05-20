// SPDX-License-Identifier: Apache-2.0

// Package status renders the alertmanager status pane: cluster
// state, version info, uptime, and the raw `config.original` YAML.
// Three anchor keys (c/p/v) scroll to the cluster / version /
// config sections respectively.
package status

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Page renders the AM /status output.
type Page struct {
	styles *theme.Styles
	tenant string

	have   bool
	st     backend.Status
	scroll int // first visible line index

	// bodyHeight is the viewport size snapshotted on the most
	// recent View call. Ctrl+D / Ctrl+U step half this; Ctrl+F /
	// Ctrl+B step body-2 (vim's CTRL-F convention). Zero before
	// the first render — handlers fall back to 10 / 20.
	bodyHeight int
}

// New constructs an empty status page.
func New(styles *theme.Styles, tenant string) *Page {
	return &Page{styles: styles, tenant: tenant}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "status" }

// Title implements app.Page. Mirrors the rest of the list pages —
// `status(<scope>)`. Empty scope folds to "all" rather than dropping
// the parenthesised label entirely so the title stays the same shape
// regardless of how many backends are configured.
func (p *Page) Title() string {
	scope := p.tenant
	if scope == "" {
		scope = "all"
	}
	return "status(" + scope + ")"
}

// HeaderContent implements app.Page. Uptime is humanised via
// timerender.Duration so the header zone stays narrow for the
// long-uptime case: a 10-year-old backend would otherwise render
// as the raw Go Stringer "87600h0m0s".
func (p *Page) HeaderContent() string {
	if !p.have {
		return "status: (loading)"
	}
	return fmt.Sprintf("%s · v%s · uptime %s",
		p.tenant, p.st.Version.Version, timerender.Duration(p.st.Uptime))
}

// Footer implements app.Page. Status pane doesn't surface
// ambient state in the bottom border.
func (*Page) Footer() string { return "" }

// PollResources implements app.PollAwarePage. The status page
// subscribes to the "status" resource so the wire-layer poller
// emits a DataMsg{Resource: backend.Status} every interval and
// the page renders fresh version / uptime / config instead of
// the cold-start snapshot for the whole session. The Update branch
// type-asserts m.Resource to backend.Status; see
// cmd/tui.go backendFetchers for the matching poll fetcher.
func (*Page) PollResources() []string { return []string{"status"} }

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
	case app.ScopeChangedMsg:
		// The status page polls a single backend today (multi-
		// backend status is post-v0.1), so a global scope switch
		// only updates the title's `(<scope>)` label — the body
		// keeps showing whatever the last poll returned. When the
		// poll plumbing fans out per-backend the existing label
		// will correctly attribute the body to the new backend
		// without further changes here.
		p.tenant = m.Scope
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
		p.scroll = min(p.scroll+cursor.HalfPageStep(p.bodyHeight), max(len(lines)-1, 0))
	case "ctrl+u":
		p.scroll = max(p.scroll-cursor.HalfPageStep(p.bodyHeight), 0)
	case "ctrl+f":
		p.scroll = min(p.scroll+cursor.FullPageStep(p.bodyHeight), max(len(lines)-1, 0))
	case "ctrl+b":
		p.scroll = max(p.scroll-cursor.FullPageStep(p.bodyHeight), 0)
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
	p.bodyHeight = height
	all := p.lines()
	if len(all) == 0 {
		// Render bg-less so the empty state matches the regular
		// status view's framing — both use the terminal default
		// background. styles.Body.Default would paint the body
		// palette behind the empty pane, which renders as a
		// coloured patch the populated view doesn't have, breaking
		// the visual parity between "loading" and "loaded" frames.
		return lipgloss.NewStyle().Width(width).Height(height).Render("status: (no data)")
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
