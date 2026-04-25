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
		{Key: "?", Description: "help", View: ""},
	}
}

// Update implements app.Page. Esc is intentionally NOT handled
// here — the App's global LayerGlobal Esc binding pops the stack
// (#23), which is exactly the right behaviour for a detail page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
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

// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	sections := []string{
		p.renderSummary(),
		"",
		"Labels:",
		renderKV(p.a.Labels),
		"",
		"Annotations:",
		renderKV(p.a.Annotations),
	}
	if p.a.GeneratorURL != "" {
		sections = append(sections, "", "Generator URL: "+p.a.GeneratorURL)
	}
	body := strings.Join(sections, "\n")
	return lipgloss.NewStyle().Width(width).Render(body)
}

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

// renderKV renders a map as sorted "  key: value" lines so the
// output is reproducible across runs (Go map iteration is
// randomised). Empty maps render as "  (none)".
func renderKV(m map[string]string) string {
	if len(m) == 0 {
		return "  (none)"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  ")
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(m[k])
	}
	return b.String()
}

// flashFn is a tiny constructor for FlashShowMsg-emitting Cmds so
// the action handlers stay one-liners.
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}
