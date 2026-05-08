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
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/yamlstyle"
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

	// bodyHeight is the viewport size snapshotted on the most
	// recent View call. Ctrl+D/U step half it; Ctrl+F/B step
	// body-2. Zero before the first render — handlers fall back to
	// 10 / 20.
	bodyHeight int
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
		p.scroll += cursor.HalfPageStep(p.bodyHeight)
	case "ctrl+u":
		p.scroll = max(p.scroll-cursor.HalfPageStep(p.bodyHeight), 0)
	case "ctrl+f":
		p.scroll += cursor.FullPageStep(p.bodyHeight)
	case "ctrl+b":
		p.scroll = max(p.scroll-cursor.FullPageStep(p.bodyHeight), 0)
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
	out = append(out, splitLines(yamlstyle.Body(p.backendYAML, p.styles))...)
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
		out = append(out, splitLines(yamlstyle.Body(p.amConfig, p.styles))...)
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

// redactionMarker is the placeholder rendered in place of every
// secret field. Pinned as a constant so a future tweak (e.g.
// "<redacted>") edits one site.
const redactionMarker = "***"

// redactedBackendYAML marshals cfg with secrets masked. Fields that
// carry credentials (basic_auth.password, authorization.credentials,
// bearer_token, every value of headers) are replaced with the
// redactionMarker so a glance at the page never leaks them — every
// other field is left unchanged.
func redactedBackendYAML(cfg config.Backend) (string, error) {
	out := cfg
	out.BasicAuth = redactBasic(cfg.BasicAuth)
	out.Authorization = redactAuthorization(cfg.Authorization)
	if cfg.BearerToken != "" {
		out.BearerToken = redactionMarker
	}
	out.Headers = redactHeaders(cfg.Headers)
	body, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshal backend: %w", err)
	}
	return string(body), nil
}

func redactBasic(in *config.BasicAuth) *config.BasicAuth {
	if in == nil {
		return nil
	}
	out := *in
	if out.Password != "" {
		out.Password = redactionMarker
	}
	out.Username = partialRedact(out.Username)
	return &out
}

// partialRedact masks a non-secret-but-identifying field. Audit
// F12: BasicAuth.Username was previously emitted unchanged in the
// tenantconfig inspector, leaking the configured account name to
// over-the-shoulder observers. The fix keeps the first two
// characters visible when the field is at least four characters
// long ("admin" -> "ad***", "prod-svc" -> "pr***") so the operator
// can still disambiguate between similarly-shaped configs (prod
// vs. staging vs. dev) without giving up the full identifier.
// Shorter strings fully redact — exposing two of two characters
// would be no redaction at all.
func partialRedact(s string) string {
	if s == "" {
		return ""
	}
	const exposeLen = 2
	const minLen = 4
	if len(s) < minLen {
		return redactionMarker
	}
	return s[:exposeLen] + redactionMarker
}

func redactAuthorization(in *config.Authorization) *config.Authorization {
	if in == nil {
		return nil
	}
	out := *in
	if out.Credentials != "" {
		out.Credentials = redactionMarker
	}
	return &out
}

// redactHeaders masks every value in the user-supplied headers map.
// We cannot tell from the schema which entries are secrets and which
// are plain identifiers (e.g. an X-Trace-Id), so the conservative
// choice — mask all of them — is the only one consistent with the
// "never leak credentials at a glance" contract. Operators who want
// a non-redacted view can read the source YAML directly.
func redactHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if v == "" {
			out[k] = ""
			continue
		}
		out[k] = redactionMarker
	}
	return out
}
