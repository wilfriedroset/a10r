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
	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/output"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/clipboard"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/yamlstyle"
)

// Options bundles the per-page dependencies.
type Options struct {
	Silence backend.Silence
	Tenant  string
	Styles  *theme.Styles
	// Clipboard handles `Y` (copy field); nil defaults to OSC52.
	Clipboard clipboard.Clipboard
}

// Page is the silence-detail view. Implements app.Page.
type Page struct {
	*detailpage.Base

	s      backend.Silence
	tenant string
	styles *theme.Styles
	clip   clipboard.Clipboard

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
}

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
		Base:    &detailpage.Base{},
		s:       opts.Silence,
		tenant:  opts.Tenant,
		styles:  opts.Styles,
		clip:    clipboard.Resolve(opts.Clipboard),
		body:    body,
		rawBody: raw,
	}
}

func (*Page) Crumb() string { return "silence" }

// Title is "Describe(<scope>/<id>)" — same shape as alert-detail.
// Appends ` [raw yaml]` when `y` has toggled raw mode; both modes
// render as YAML so the marker is the only visual difference.
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

// Bindings implements app.Page. The page exposes the `y` raw-YAML
// toggle (k9s convention) and the `Y` field picker. Neither mutates
// remote state, so neither is Dangerous and read-only mode keeps
// both. Scroll keys ride on the global vim-motion list — no need to
// advertise them here.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "y", Description: "yaml", View: "silence"},
		{Key: "Y", Description: "copy field", View: "silence"},
	}
}

// Update implements app.Page. Esc is intentionally NOT handled
// here — the App's global LayerGlobal Esc binding pops the stack,
// which is exactly the right behaviour for a detail page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	// Origin gates the handler so another page's picker forwarded
	// down here cannot drive a copy, the same guard the silence form
	// puts on its tenant picker.
	if m, ok := msg.(modal.PickerSubmittedMsg); ok {
		if m.Origin != clipboard.PickerOrigin {
			return p, nil
		}
		return p, clipboard.CopySelected(p.clip, p.copyFields(), m)
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	key := keyMsg.String()
	if key == "Y" {
		return p, clipboard.OpenPicker(p.copyFields())
	}
	if key == "y" {
		// Toggle between the curated structured YAML and the raw
		// backend.Silence dump. Reset scroll so the user lands at
		// the top of the new mode rather than mid-document at an
		// offset that came from a body of a different length.
		p.rawYAML = !p.rawYAML
		p.Scroll = 0
		return p, nil
	}
	p.HandleScrollKey(key)
	return p, nil
}

// View implements app.Page. Builds the styled YAML, slices the
// visible window via detailpage.Base.Visible, and width-pads through
// listpage.Wrap so a long matcher value doesn't bleed across borders.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	visible := p.Visible(p.bodyLines(), height)
	return listpage.Wrap(width, strings.Join(visible, "\n"))
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
	State     string        `yaml:"state"`
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
		return "", fmt.Errorf("marshal silence yaml: %w", err)
	}
	return string(out), nil
}

// marshalRawSilence renders the cached backend.Silence via
// output.WriteYAML — the k9s "what does the API actually return?"
// escape hatch. Distinct from marshalSilence: no curated key set,
// no RFC3339 formatting, no zero-value omission.
func marshalRawSilence(s backend.Silence) (string, error) {
	var buf bytes.Buffer
	if err := output.WriteYAML(&buf, s); err != nil {
		return "", fmt.Errorf("marshal raw silence: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// copyFields is the `Y` picker's list. The whole selector comes
// first, then one row per matcher, so a user can lift either the
// block the silence form's matcher textarea reads back or a single
// matcher. The block is newline-joined because matcher.Parse splits
// on newlines only: a comma-joined line parses as one corrupt
// matcher rather than failing. Times are RFC 3339, the shape the
// structured body already shows.
func (p *Page) copyFields() []clipboard.Field {
	parts := make([]string, len(p.s.Matchers))
	for i, m := range p.s.Matchers {
		parts[i] = matcher.Format(m)
	}
	out := make([]clipboard.Field, 0, 7+len(parts))
	out = append(out,
		clipboard.Field{Name: "id", Value: p.s.ID},
		clipboard.Field{Name: "creator", Value: p.s.CreatedBy},
		clipboard.Field{Name: "comment", Value: p.s.Comment},
		clipboard.Field{Name: "matchers", Value: strings.Join(parts, "\n")},
	)
	for i, m := range p.s.Matchers {
		out = append(out, clipboard.Field{Name: "matcher " + m.Name, Value: parts[i]})
	}
	return append(out,
		clipboard.Field{Name: "startsAt", Value: p.s.StartsAt.UTC().Format(time.RFC3339)},
		clipboard.Field{Name: "endsAt", Value: p.s.EndsAt.UTC().Format(time.RFC3339)},
		clipboard.Field{Name: "state", Value: string(p.s.State)},
	)
}
