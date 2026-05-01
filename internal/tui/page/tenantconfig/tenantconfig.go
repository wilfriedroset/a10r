// SPDX-License-Identifier: Apache-2.0

// Package tenantconfig renders a read-only inspector for one
// configured backend: the redacted a10r.yaml entry plus the
// Alertmanager `config.original` route tree fetched from
// /api/v2/status. Reachable from the tenant page on Enter.
package tenantconfig

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// StatusFetcher is the small surface the page needs to resolve
// /api/v2/status against the page's tenant. backend.Client
// satisfies it for free; tests inject a recording fake.
type StatusFetcher interface {
	Status(ctx context.Context) (backend.Status, error)
}

// Options bundles the constructor inputs.
type Options struct {
	// Tenant is the configured backend name displayed in the title.
	Tenant string
	// Backend is the resolved a10r.yaml entry. Rendered through the
	// redacted yaml so secrets never reach the screen.
	Backend config.Backend
	// Fetcher resolves the AM `config.original` for the tenant.
	// nil disables the AM section gracefully — the page renders
	// "(no client available)" instead of crashing.
	Fetcher StatusFetcher
	// Styles is the compiled theme.
	Styles theme.Styles
	// FetchTimeout caps the AM /status round-trip. Zero defaults to
	// 30s, matching the vanilla client's request timeout.
	FetchTimeout time.Duration
}

// Page is the tenant inspector.
type Page struct {
	tenant       string
	styles       theme.Styles
	backendYAML  string
	amConfig     string
	amErr        error
	loading      bool
	fetcher      StatusFetcher
	fetchTimeout time.Duration

	scroll int
}

// statusFetchedMsg carries the result of the lazy /api/v2/status
// fetch back into Update. err set means the fetch failed; cfg is
// the YAML body otherwise.
type statusFetchedMsg struct {
	cfg string
	err error
}

// New constructs the inspector. The redacted backend yaml is
// built eagerly so the screen renders immediately; the AM config
// is fetched lazily via Init's Cmd.
func New(opts Options) *Page {
	body, err := redactedBackendYAML(opts.Backend)
	if err != nil {
		body = fmt.Sprintf("(failed to render a10r config: %v)", err)
	}
	timeout := opts.FetchTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Page{
		tenant:       opts.Tenant,
		styles:       opts.Styles,
		backendYAML:  body,
		fetcher:      opts.Fetcher,
		fetchTimeout: timeout,
		loading:      opts.Fetcher != nil,
	}
}

// Init implements app.Page. Kicks the AM /status fetch when a
// fetcher is wired; without one, the AM section renders a static
// "(no client available)" message.
func (p *Page) Init() tea.Cmd {
	if p.fetcher == nil {
		return nil
	}
	fetcher := p.fetcher
	timeout := p.fetchTimeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		st, err := fetcher.Status(ctx)
		if err != nil {
			return statusFetchedMsg{err: err}
		}
		return statusFetchedMsg{cfg: st.Config}
	}
}

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "tenant-config" }

// Title implements app.Page.
func (p *Page) Title() string {
	scope := p.tenant
	if scope == "" {
		scope = "—"
	}
	return "tenant-config(" + scope + ")"
}

// HeaderContent implements app.Page.
func (p *Page) HeaderContent() string {
	if p.loading {
		return "fetching alertmanager config…"
	}
	return ""
}

// Footer implements app.Page.
func (*Page) Footer() string { return "" }

// Bindings implements app.Page. The page is read-only; only
// scroll motions are surfaced to keep the hint strip sparse.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Esc", Description: "back", View: "tenant-config"},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case statusFetchedMsg:
		p.loading = false
		if m.err != nil {
			p.amErr = m.err
			return p, nil
		}
		p.amConfig = m.cfg
		return p, nil
	case app.GoToFirstRowMsg:
		p.scroll = 0
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch keyMsg.String() {
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
		p.scroll = 1 << 30 // renderer clamps against body length
	}
	return p, nil
}

// View implements app.Page. Builds two YAML-styled sections,
// scrolled by p.scroll. Long files are rendered in full so the
// user can :%search-style scan; clamping happens at the bottom.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
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

// bodyLines composes the two sections into a flat line list.
// Section headers use theme.YAML.Key so the user's eye lands on
// them; bodies render as styled YAML key/value pairs via the
// existing skin roles.
func (p *Page) bodyLines() []string {
	out := make([]string, 0, 32)
	header := func(s string) string {
		return p.styles.YAML.Key.Render(s)
	}
	out = append(out, header("# a10r config"))
	out = append(out, splitLines(styleYAML(p.backendYAML, p.styles))...)
	out = append(out, "", header("# alertmanager config (config.original)"))
	switch {
	case p.fetcher == nil:
		out = append(out, "(no client available)")
	case p.loading:
		out = append(out, "(fetching…)")
	case p.amErr != nil:
		out = append(out, fmt.Sprintf("(fetch failed: %v)", p.amErr))
	case strings.TrimSpace(p.amConfig) == "":
		out = append(out, "(empty)")
	default:
		out = append(out, splitLines(styleYAML(p.amConfig, p.styles))...)
	}
	return out
}

// splitLines is the standard "split on \n" helper, with the
// empty-string-yields-empty-slice property so the joiner doesn't
// inject a stray blank line at the head.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// styleYAML applies theme.YAML.Key / .Punct / .Value to the
// "key: value" structure. Best-effort line-level: a `key:` prefix
// on a line tints the key; trailing value gets the value style.
// Lines that don't match the simple pattern (lists, multi-line
// values) render with the default body fg so structure stays
// legible without the renderer needing a full YAML AST walker.
func styleYAML(body string, styles theme.Styles) string {
	if body == "" {
		return ""
	}
	out := make([]string, 0, strings.Count(body, "\n")+1)
	for line := range strings.SplitSeq(body, "\n") {
		out = append(out, styleYAMLLine(line, styles))
	}
	return strings.Join(out, "\n")
}

// styleYAMLLine applies skin colours to one YAML line. Pure so
// it's easy to test in isolation. Best-effort: comment-only lines
// pass through unstyled (otherwise the leading "# resolved at" of
// "# resolved at: 2026-…" would tint as a key) and lines without
// a `:` short-circuit too.
func styleYAMLLine(line string, styles theme.Styles) string {
	if isCommentLine(line) {
		return line
	}
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line
	}
	prefixEnd := 0
	for prefixEnd < idx && (line[prefixEnd] == ' ' || line[prefixEnd] == '-') {
		prefixEnd++
	}
	indent := line[:prefixEnd]
	key := line[prefixEnd:idx]
	rest := line[idx:]
	punctEnd := 1
	if punctEnd < len(rest) && rest[punctEnd] == ' ' {
		punctEnd++
	}
	punct := rest[:punctEnd]
	value := rest[punctEnd:]
	styled := indent + styles.YAML.Key.Render(key) + styles.YAML.Punct.Render(punct)
	if value != "" {
		styled += styles.YAML.Value.Render(value)
	}
	return styled
}

// isCommentLine reports whether the line's first non-whitespace
// (or non-list-marker) character is `#`. Skips leading spaces and
// `-` so list-element comments ("- # foo") and indented comments
// pass through too.
func isCommentLine(line string) bool {
	trimmed := strings.TrimLeft(line, " -")
	return strings.HasPrefix(trimmed, "#")
}

// redactionMarker is the placeholder rendered in place of every
// secret field. Pinned as a constant so a future tweak (e.g.
// "<redacted>") edits one site.
const redactionMarker = "***"

// redactedBackendYAML marshals cfg with secrets masked. Auth
// fields that carry credentials are replaced with the
// redactionMarker so a glance at the page never leaks them —
// every other field is left unchanged.
func redactedBackendYAML(cfg config.Backend) (string, error) {
	out := cfg
	out.Auth = redactAuth(cfg.Auth)
	body, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshal backend: %w", err)
	}
	return string(body), nil
}

// redactAuth deep-copies the auth spec and masks every secret
// field. nil input returns nil so cfg.Auth's nullable contract
// survives the round-trip — yaml.Marshal then omits the auth
// block entirely.
func redactAuth(in *config.AuthSpec) *config.AuthSpec {
	if in == nil {
		return nil
	}
	out := *in
	out.Basic = redactBasic(in.Basic)
	out.Bearer = redactBearer(in.Bearer)
	out.Header = redactHeader(in.Header)
	return &out
}

func redactBasic(in *config.BasicAuth) *config.BasicAuth {
	if in == nil {
		return nil
	}
	out := *in
	if out.Password != "" {
		out.Password = redactionMarker
	}
	return &out
}

func redactBearer(in *config.BearerAuth) *config.BearerAuth {
	if in == nil {
		return nil
	}
	out := *in
	if out.Token != "" {
		out.Token = redactionMarker
	}
	return &out
}

func redactHeader(in *config.HeaderAuth) *config.HeaderAuth {
	if in == nil {
		return nil
	}
	out := *in
	if out.Value != "" {
		out.Value = redactionMarker
	}
	return &out
}
