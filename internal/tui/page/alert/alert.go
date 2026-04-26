// SPDX-License-Identifier: Apache-2.0

// Package alert renders the alert-detail page — a read-only view
// of one cached backend.Alert pushed from the alerts list row.
// Per C5 there is no extra GET on push: the alerts list snapshot
// is sufficient and a poll tick will refresh it on its own
// schedule.
package alert

import (
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Clipboard is the small surface the page needs to copy strings.
// Production wraps a real OS clipboard binding; tests inject a
// recorder. Errors flow through to the user as flash messages.
type Clipboard interface {
	Copy(s string) error
}

// Browser is the small surface the page needs to open a URL.
// Production wraps `browser.OpenURL` or similar; tests inject a
// recorder. Errors flow through to the user as flash messages.
type Browser interface {
	Open(url string) error
}

// Options bundles the per-page dependencies.
type Options struct {
	// Alert is the cached object to render. Required.
	Alert backend.Alert
	// Tenant is the source-backend tag for the header strip.
	Tenant string
	// Styles is the compiled theme.
	Styles theme.Styles
	// Clipboard handles the `y` (copy fingerprint) action. nil
	// disables the binding gracefully — `y` flashes a "no clipboard
	// integration" hint instead of crashing.
	Clipboard Clipboard
	// Browser handles the `o` (open generatorURL) action. nil
	// disables the binding the same way.
	Browser Browser
	// Now injects the clock used by the age line. nil falls back
	// to time.Now.
	Now func() time.Time
}

// Page is the alert-detail view. Implements app.Page.
type Page struct {
	a       backend.Alert
	tenant  string
	styles  theme.Styles
	clip    Clipboard
	browser Browser
	now     func() time.Time

	// scroll is the index of the first visible body line. j/k/G/gg
	// walk it; the renderer reconciles against the body height
	// every frame so the user can never scroll past the bottom.
	scroll int
}

// New constructs an alert-detail page.
func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Page{
		a:       opts.Alert,
		tenant:  opts.Tenant,
		styles:  opts.Styles,
		clip:    opts.Clipboard,
		browser: opts.Browser,
		now:     now,
	}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "detail" }

// Title implements app.Page — "Describe(<scope>/<alertname>)"
// mirrors the k9s pod-detail header.
func (p *Page) Title() string {
	scope := p.tenant
	if scope == "" {
		scope = "—"
	}
	return "Describe(" + scope + "/" + p.a.Labels["alertname"] + ")"
}

// HeaderContent implements app.Page. Shows alertname + state +
// source backend so the header strip identifies the active alert
// at a glance.
func (p *Page) HeaderContent() string {
	parts := []string{p.a.Labels["alertname"], string(p.a.State)}
	if p.tenant != "" {
		parts = append(parts, p.tenant)
	}
	return strings.Join(parts, " · ")
}

// Bindings implements app.Page.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "s", Description: "silence", View: "alert", Dangerous: true},
		{Key: "y", Description: "copy fp", View: "alert"},
		{Key: "o", Description: "open URL", View: "alert"},
	}
}

// Update implements app.Page. Esc is intentionally NOT handled
// here — the App's global LayerGlobal Esc binding pops the stack
// (#23), which is exactly the right behaviour for a detail page.
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
		cmd := p.copyFingerprint()
		return p, cmd
	case "o":
		cmd := p.openGeneratorURL()
		return p, cmd
	case "j", "down":
		p.scroll++
	case "k", "up":
		if p.scroll > 0 {
			p.scroll--
		}
	case "ctrl+d":
		p.scroll += 10
	case "ctrl+u":
		p.scroll = max(p.scroll-10, 0)
	case "G":
		// Pin the last line; the renderer clamps against the
		// actual body length on the next frame.
		p.scroll = 1 << 30
	case "s":
		// Silence form lands in #30. Until then the binding flashes
		// the same placeholder the alerts list uses so the affordance
		// is consistent across pages.
		return p, func() tea.Msg {
			return footer.FlashShowMsg{Level: footer.FlashWarn, Text: "silence form arrives in #30"}
		}
	}
	return p, nil
}

// copyFingerprint returns the Cmd that asks the clipboard
// integration to copy this alert's fingerprint, surfacing success
// or failure as a flash. nil clipboard is a graceful "no
// integration" path.
func (p *Page) copyFingerprint() tea.Cmd {
	if p.clip == nil {
		return flashFn(footer.FlashWarn, "clipboard not configured")
	}
	if p.a.Fingerprint == "" {
		return flashFn(footer.FlashWarn, "alert has no fingerprint")
	}
	if err := p.clip.Copy(p.a.Fingerprint); err != nil {
		return flashFn(footer.FlashError, "copy failed: "+err.Error())
	}
	return flashFn(footer.FlashSuccess, "fingerprint copied")
}

// openGeneratorURL asks the browser integration to open the
// alert's generatorURL. Missing URL is a soft no-op with a hint
// (alerts without a generator URL are entirely valid per the AM
// schema, just less linkable).
func (p *Page) openGeneratorURL() tea.Cmd {
	if p.a.GeneratorURL == "" {
		return flashFn(footer.FlashInfo, "this alert has no generator URL")
	}
	if p.browser == nil {
		return flashFn(footer.FlashWarn, "browser not configured")
	}
	if err := p.browser.Open(p.a.GeneratorURL); err != nil {
		return flashFn(footer.FlashError, "open failed: "+err.Error())
	}
	return flashFn(footer.FlashSuccess, "opened in browser")
}

// View implements app.Page. Builds a flat line list, hanging-
// indent-wraps any line that overflows width, then slices the
// visible window starting at p.scroll.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := p.bodyLines(width)
	p.reconcileScroll(len(lines), height)
	end := min(p.scroll+height, len(lines))
	if p.scroll > end {
		p.scroll = end
	}
	visible := lines[p.scroll:end]
	return strings.Join(visible, "\n")
}

// bodyLines builds the full list of rendered lines (one display
// row each) so View can slice and the scroll machinery can clamp
// against an exact length. Long values are wrapped with a
// hanging indent so continuation lines align under the value
// instead of bleeding to column 0.
func (p *Page) bodyLines(width int) []string {
	out := make([]string, 0, 32)
	out = append(out, splitLines(p.renderSummary())...)
	out = append(out, "", "Labels:")
	out = append(out, kvLines(p.a.Labels, width)...)
	out = append(out, "", "Annotations:")
	out = append(out, kvLines(p.a.Annotations, width)...)
	if p.a.GeneratorURL != "" {
		out = append(out, "")
		out = append(out, wrapHanging("Generator URL: "+p.a.GeneratorURL, width, len("Generator URL: "))...)
	}
	return out
}

// reconcileScroll clamps p.scroll so the visible window stays
// within [0, totalLines). Mirrors the list pages' viewport
// reconciliation but operates on flat-line indices instead of
// row indices.
func (p *Page) reconcileScroll(totalLines, height int) {
	if p.scroll < 0 {
		p.scroll = 0
		return
	}
	maxScroll := max(totalLines-height, 0)
	if p.scroll > maxScroll {
		p.scroll = maxScroll
	}
}

// splitLines splits s on \n. Used so renderSummary's multi-line
// output joins naturally with the section headers in bodyLines.
func splitLines(s string) []string { return strings.Split(s, "\n") }

// renderSummary is the top block: alertname, state, severity,
// fingerprint, age. Each on its own line for readability.
func (p *Page) renderSummary() string {
	lines := []string{
		"alertname:   " + p.a.Labels["alertname"],
		"state:       " + string(p.a.State),
	}
	if v, ok := p.a.Labels["severity"]; ok {
		lines = append(lines, "severity:    "+v)
	}
	if p.a.Fingerprint != "" {
		lines = append(lines, "fingerprint: "+p.a.Fingerprint)
	}
	if age := header.FormatAge(p.now(), p.a.StartsAt); age != "" {
		lines = append(lines, "age:         "+age)
	}
	if p.tenant != "" {
		lines = append(lines, "tenant:      "+p.tenant)
	}
	return strings.Join(lines, "\n")
}

// kvLines renders a map as sorted "  key: value" lines. Embedded
// "\n" characters in a value (common in Prometheus-style
// annotations like "VALUE = 0\nLABELS = …") are honoured —
// each line of the value is rendered as its own row with the
// same hanging indent as wrap continuations, so multi-line
// values read as one visually-aligned block under the value
// column. Empty maps render as a single "  (none)" line.
func kvLines(m map[string]string, width int) []string {
	if len(m) == 0 {
		return []string{"  (none)"}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		prefix := "  " + k + ": "
		hangCols := lipgloss.Width(prefix)
		hang := strings.Repeat(" ", hangCols)
		for vi, segment := range strings.Split(m[k], "\n") {
			leading := hang
			if vi == 0 {
				leading = prefix
			}
			out = append(out, wrapHanging(leading+segment, width, hangCols)...)
		}
	}
	return out
}

// wrapHanging breaks s into lines that fit width columns, with
// continuation lines indented by hangingCols spaces so wrapped
// values stay visually aligned with the first line. Word-wraps
// at whitespace where possible; falls back to a hard cut when a
// single word exceeds the available column budget — or, crucially,
// when the only whitespace in rest sits inside the hanging indent
// (a long no-internal-whitespace value would otherwise loop
// forever cutting only the indent and never the content).
func wrapHanging(s string, width, hangingCols int) []string {
	if width <= 0 {
		return []string{s}
	}
	if lipgloss.Width(s) <= width {
		return []string{s}
	}
	hang := strings.Repeat(" ", hangingCols)

	var out []string
	rest := s
	limit := width
	for lipgloss.Width(rest) > limit {
		cut := bestBreakIndex(rest, limit)
		// Forward-progress guard: a cut at or before the leading
		// indent yields a no-content line and never shrinks rest.
		// Force a hard cut at the limit in that case so the loop
		// always makes progress.
		if cut <= hangingCols {
			cut = hardCutAt(rest, limit)
		}
		if cut <= 0 {
			break // pathological input; emit what we have
		}
		out = append(out, rest[:cut])
		rest = hang + strings.TrimLeft(rest[cut:], " ")
	}
	out = append(out, rest)
	return out
}

// hardCutAt returns the byte index in s at which the leading
// slice fits within limit columns. Used as the forward-progress
// fallback when bestBreakIndex's whitespace-aware result would
// stall the wrap loop.
func hardCutAt(s string, limit int) int {
	width := 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > limit {
			return i
		}
		width += rw
	}
	return len(s)
}

// bestBreakIndex returns the byte index in s at which to split
// so the leading slice fits within limit columns. Prefers the
// last whitespace boundary at-or-before the limit; falls back to
// a hard cut at the limit when a single word overflows it.
func bestBreakIndex(s string, limit int) int {
	if lipgloss.Width(s) <= limit {
		return len(s)
	}
	// Walk forward tracking width; remember the last whitespace
	// position. When width passes limit, break at that whitespace
	// or, failing that, at the current position.
	width := 0
	lastWS := -1
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > limit {
			if lastWS > 0 {
				return lastWS
			}
			return i
		}
		if r == ' ' {
			lastWS = i
		}
		width += rw
	}
	return len(s)
}

// flashFn is a tiny constructor for FlashShowMsg-emitting Cmds so
// the action handlers stay one-liners.
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}
