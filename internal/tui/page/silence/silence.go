// SPDX-License-Identifier: Apache-2.0

// Package silence renders the silence-detail page — a read-only
// YAML view of one cached backend.Silence pushed from the silences
// list row. Mirrors the alert-detail page's shape: no extra GET on
// push (the silences list snapshot is sufficient and the next poll
// tick refreshes it on its own schedule); scrolls with j/k/G plus
// Ctrl+D/U/F/B; Esc is handled by the App's global LayerGlobal
// binding.
package silence

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/output"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/yamlstyle"
)

// Options bundles the per-page dependencies.
type Options struct {
	// Silence is the cached object to render. Required.
	Silence backend.Silence
	// Tenant is the source-backend tag for the header strip.
	Tenant string
	// Styles is the compiled theme.
	Styles *theme.Styles
}

// Page is the silence-detail view. Implements app.Page.
type Page struct {
	s      backend.Silence
	tenant string
	styles *theme.Styles

	// body is the pre-marshalled YAML body. Computed once at
	// construction so re-renders don't re-marshal on every frame.
	body string

	// rawBody is the pre-marshalled raw-payload YAML body — a
	// straight output.WriteYAML dump of the cached backend.Silence
	// struct, rendered when the user presses `y` to flip into the
	// k9s-style raw view. Pre-marshalled at construction for the
	// same reason as `body`: re-rendering on every View would burn
	// CPU on a static payload.
	rawBody string

	// rawYAML toggles between body (default) and rawBody. The flip
	// is per-page (does not persist across pushes); a fresh drill
	// always opens structured because that's the curated shape.
	rawYAML bool

	// scroll is the index of the first visible body line. j/k/G/gg
	// walk it; the renderer reconciles against the body height
	// every frame so the user can never scroll past the bottom.
	scroll int

	// bodyHeight is the viewport size snapshotted on the most
	// recent View call. Ctrl+D/U step half it; Ctrl+F/B step
	// body-2. Zero before the first render — handlers fall back to
	// 10 / 20.
	bodyHeight int
}

// New constructs a silence-detail page.
func New(opts Options) *Page {
	body, err := marshalSilence(opts.Silence)
	if err != nil {
		body = fmt.Sprintf("(failed to render silence: %v)", err)
	}
	raw, rawErr := marshalRawSilence(opts.Silence)
	if rawErr != nil {
		raw = fmt.Sprintf("(failed to render raw silence: %v)", rawErr)
	}
	return &Page{
		s:       opts.Silence,
		tenant:  opts.Tenant,
		styles:  opts.Styles,
		body:    body,
		rawBody: raw,
	}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "silence" }

// Title implements app.Page — "Describe(<scope>/<id>)" mirrors the
// alert-detail header so the two read consistently. Appends
// ` [raw yaml]` when the page is in the `y`-toggled raw mode so the
// operator can tell at a glance which view they're looking at —
// the structured curated view and the raw payload both render as
// YAML, so without the marker the two modes are visually identical
// except for the body content.
func (p *Page) Title() string {
	scope := p.tenant
	if scope == "" {
		scope = "—"
	}
	base := "Describe(" + scope + "/" + p.s.ID + ")"
	if p.rawYAML {
		base += " [raw yaml]"
	}
	return base
}

// HeaderContent implements app.Page. The title already shows
// `<tenant>/<id>` and the YAML body surfaces `state:` on its own
// line — anything else here would duplicate what's a glance away.
func (*Page) HeaderContent() string { return "" }

// Footer implements app.Page. Silence detail doesn't surface
// ambient state in the bottom border.
func (*Page) Footer() string { return "" }

// Bindings implements app.Page. The only verb the silence-detail
// page exposes is the `y` raw-YAML toggle (k9s convention). Scroll
// keys ride on the global vim-motion list — no need to advertise
// them here.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "y", Description: "yaml", View: "silence"},
	}
}

// Update implements app.Page. Esc is intentionally NOT handled
// here — the App's global LayerGlobal Esc binding pops the stack,
// which is exactly the right behaviour for a detail page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if _, ok := msg.(app.GoToFirstRowMsg); ok {
		p.scroll = 0
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch keyMsg.String() {
	case "y":
		// Toggle between the curated structured YAML and the raw
		// backend.Silence dump. Reset scroll so the user lands at
		// the top of the new mode rather than mid-document at an
		// offset that came from a body of a different length.
		p.rawYAML = !p.rawYAML
		p.scroll = 0
	case "j", "down":
		p.scroll++
	case "k", "up":
		if p.scroll > 0 {
			p.scroll--
		}
	case "ctrl+d":
		p.scroll += cursor.HalfPageStep(p.bodyHeight)
	case "ctrl+u":
		p.scroll = max(p.scroll-cursor.HalfPageStep(p.bodyHeight), 0)
	case "ctrl+f":
		p.scroll += cursor.FullPageStep(p.bodyHeight)
	case "ctrl+b":
		p.scroll = max(p.scroll-cursor.FullPageStep(p.bodyHeight), 0)
	case "G":
		// Pin the last line; the renderer clamps against the actual
		// body length on the next frame.
		p.scroll = 1 << 30
	}
	return p, nil
}

// View implements app.Page. Builds the styled YAML, slices the
// visible window starting at p.scroll. Width clamps the body so a
// long matcher value doesn't bleed across the borders.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	p.bodyHeight = height
	lines := p.bodyLines()
	if p.scroll < 0 {
		p.scroll = 0
	}
	maxScroll := max(len(lines)-height, 0)
	if p.scroll > maxScroll {
		p.scroll = maxScroll
	}
	end := min(p.scroll+height, len(lines))
	visible := lines[p.scroll:end]
	return lipgloss.NewStyle().Width(width).Render(strings.Join(visible, "\n"))
}

// bodyLines returns the styled YAML split per line so View can
// slice and scroll machinery can clamp against an exact length.
// Branches on p.rawYAML so the same scroll / styling pipeline
// drives both modes.
func (p *Page) bodyLines() []string {
	src := p.body
	if p.rawYAML {
		src = p.rawBody
	}
	styled := yamlstyle.Body(src, p.styles)
	if styled == "" {
		return nil
	}
	return strings.Split(styled, "\n")
}

// silenceYAML is the on-disk shape used for the read-only viewer.
// RFC3339 timestamps keep the document scannable without the user
// having to re-read the AM-API epoch nuances.
type silenceYAML struct {
	ID        string        `yaml:"id"`
	State     string        `yaml:"state,omitempty"`
	CreatedBy string        `yaml:"createdBy"`
	Comment   string        `yaml:"comment"`
	StartsAt  string        `yaml:"startsAt"`
	EndsAt    string        `yaml:"endsAt"`
	UpdatedAt string        `yaml:"updatedAt,omitempty"`
	Matchers  []matcherYAML `yaml:"matchers"`
}

// matcherYAML mirrors backend.Matcher one-for-one. Same shape the
// silences package's editor round-trip uses; duplicated here so
// the two packages stay decoupled (this view is read-only and the
// editor's silenceYAML stays scoped to the write path).
type matcherYAML struct {
	Name    string `yaml:"name"`
	Value   string `yaml:"value"`
	IsRegex bool   `yaml:"isRegex"`
	IsEqual bool   `yaml:"isEqual"`
}

func marshalSilence(s backend.Silence) (string, error) {
	doc := silenceYAML{
		ID:        s.ID,
		State:     string(s.State),
		CreatedBy: s.CreatedBy,
		Comment:   s.Comment,
		StartsAt:  s.StartsAt.UTC().Format(time.RFC3339),
		EndsAt:    s.EndsAt.UTC().Format(time.RFC3339),
		Matchers:  make([]matcherYAML, len(s.Matchers)),
	}
	if !s.UpdatedAt.IsZero() {
		doc.UpdatedAt = s.UpdatedAt.UTC().Format(time.RFC3339)
	}
	for i, m := range s.Matchers {
		doc.Matchers[i] = matcherYAML{
			Name: m.Name, Value: m.Value,
			IsRegex: m.IsRegex, IsEqual: m.IsEqual,
		}
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// marshalRawSilence renders the cached backend.Silence directly
// via output.WriteYAML — the k9s-style "what does the API
// actually return?" escape hatch. Distinct from marshalSilence:
// no curated key set, no RFC3339 timestamp formatting, no zero-
// value omission. The point of the toggle is showing the raw
// payload, complete with go-default lowercased field names and
// any zero-valued timestamps the upstream emitted. Sharing the
// indent / encoder choice with the headless `--output=yaml` path
// keeps the two views internally consistent.
func marshalRawSilence(s backend.Silence) (string, error) {
	var buf bytes.Buffer
	if err := output.WriteYAML(&buf, s); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
