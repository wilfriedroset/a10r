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
	// Anchors are the optional section jumps. A report short enough
	// to read in one frame needs none.
	Anchors []Anchor
}

// Anchor is one key that scrolls a named section to the top of the
// frame. The section is found by line prefix rather than by index
// because the lines above it grow with the host's configuration.
type Anchor struct {
	Key         string
	Description string
	Prefix      string
}

// Page is the scrollable body over a rendered report.
type Page struct {
	*detailpage.Base

	title   string
	render  func() string
	anchors []Anchor
	body    []string
}

// New builds the page and renders its first body.
func New(opts Options) *Page {
	p := &Page{
		Base:    &detailpage.Base{},
		title:   opts.Title,
		render:  opts.Render,
		anchors: opts.Anchors,
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
	out := make([]action.Action, 0, len(p.anchors)+2)
	for _, a := range p.anchors {
		out = append(out, action.Action{Key: a.Key, Description: a.Description, View: p.title})
	}
	return append(out,
		action.Action{Key: "r", Description: "re-render", View: p.title},
		action.Action{Key: "Esc", Description: "back", View: p.title},
	)
}

// jump scrolls the first line carrying prefix to the top of the
// frame. A report without that section leaves the view alone, so a
// key the body has outgrown is inert rather than surprising.
func (p *Page) jump(prefix string) {
	for i, line := range p.body {
		if strings.HasPrefix(line, prefix) {
			p.Scroll = i
			return
		}
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
	key := keyMsg.String()
	if key == "r" {
		p.reRender()
		return p, nil
	}
	for _, a := range p.anchors {
		if a.Key == key {
			p.jump(a.Prefix)
			return p, nil
		}
	}
	p.HandleScrollKey(key)
	return p, nil
}

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	visible := p.Visible(p.body, height)
	return listpage.Wrap(width, strings.Join(visible, "\n"))
}
