// SPDX-License-Identifier: Apache-2.0

// Package selfreport renders a10r's own resolved state as a
// scrollable read-only page. The body text comes from a renderer the
// caller supplies, so the page shares one implementation with the
// headless reports in internal/report instead of restating them.
package selfreport

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// Options configures one self-report page.
type Options struct {
	// Title is both the bordered body's label and the breadcrumb, so
	// `:info` reads as "info" in either place.
	Title string
	// Render produces the whole body text. It is called at
	// construction and again on every `r`, so the page reports the
	// live process rather than a snapshot taken at push time.
	Render func() string
}

// Page is the scrollable body over a rendered report.
type Page struct {
	*detailpage.Base

	title  string
	render func() string
	body   []string
}

// New builds the page and renders its first body.
func New(opts Options) *Page {
	p := &Page{
		Base:   &detailpage.Base{},
		title:  opts.Title,
		render: opts.Render,
	}
	p.reRender()
	return p
}

func (p *Page) reRender() {
	if p.render == nil {
		p.body = nil
		return
	}
	// The rendered reports end in a newline; splitting on it as-is
	// would let the viewport scroll one blank line past the content.
	p.body = strings.Split(strings.TrimRight(p.render(), "\n"), "\n")
}

func (p *Page) Crumb() string { return p.title }

func (p *Page) Title() string { return p.title }

// Bindings implements app.Page. `r` is the per-page refresh every
// other page also owns; the scroll motions stay off the hint strip.
func (p *Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "r", Description: "re-render", View: p.title},
		{Key: "Esc", Description: "back", View: p.title},
	}
}

func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	if keyMsg.String() == "r" {
		p.reRender()
		return p, nil
	}
	p.HandleScrollKey(keyMsg.String())
	return p, nil
}

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	visible := p.Visible(p.body, height)
	return listpage.Wrap(width, strings.Join(visible, "\n"))
}
