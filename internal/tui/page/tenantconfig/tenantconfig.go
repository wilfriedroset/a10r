// SPDX-License-Identifier: Apache-2.0

// Package tenantconfig renders a read-only inspector for one
// configured backend: the redacted a10r.yaml entry plus the
// Alertmanager `config.original` route tree fetched from
// /api/v2/status. Reachable from the tenant page on Enter.
package tenantconfig

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
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
	Styles *theme.Styles
	// FetchTimeout caps the AM /status round-trip. Zero defaults to
	// 30s, matching the vanilla client's request timeout.
	FetchTimeout time.Duration

	// FetchCtx is the parent ctx the /api/v2/status fetch inherits.
	// Cancelling cancels the in-flight call — keeps the page in
	// lockstep with the alerts/silences pages whose BulkCtx /
	// EditorCtx already chain through cmd.Context(), so app-level
	// shutdown propagates through the ctx (not only through
	// Close). nil falls back to context.Background() — kept so
	// tests that don't pin the parent stay green.
	FetchCtx context.Context //nolint:containedctx // fetch ctx, plumbed once at construction.
}

// Page is the tenant inspector.
type Page struct {
	*detailpage.Base

	tenant       string
	styles       *theme.Styles
	backendYAML  string
	amConfig     string
	amErr        error
	loading      bool
	fetcher      StatusFetcher
	fetchTimeout time.Duration

	// cancelFetch is the cancel func for the in-flight Status fetch.
	// Guarded by mu because the goroutine in the Init Cmd sets/clears
	// it while Close() (running on the bubbletea Update goroutine)
	// reads it. Nil when no fetch is in flight.
	mu          sync.Mutex
	cancelFetch context.CancelFunc

	// fetchCtx is the parent ctx the Status fetch derives from.
	// Mirrors Options.FetchCtx — see the doc there for the
	// rationale. Nil means "no parent pinned"; Init falls back
	// to context.Background() so single-shot tests that don't
	// care about app-level propagation stay green.
	fetchCtx context.Context //nolint:containedctx // fetch ctx, plumbed once at construction.
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
	p := &Page{
		Base:         &detailpage.Base{},
		tenant:       opts.Tenant,
		styles:       opts.Styles,
		backendYAML:  body,
		fetcher:      opts.Fetcher,
		fetchTimeout: timeout,
		fetchCtx:     opts.FetchCtx,
		loading:      opts.Fetcher != nil,
	}
	p.InitCmd = p.statusFetchCmd
	return p
}

// Init implements app.Page. Delegates to Base which surfaces the
// optional InitCmd hook — kicks the AM /status fetch when a fetcher
// is wired; without one, the AM section renders a static "(no
// client available)" message.
func (p *Page) Init() tea.Cmd { return p.Base.Init() }

// statusFetchCmd is the lazy /api/v2/status fetch. Returns nil when
// no fetcher is wired so Base.Init surfaces a nil Cmd in turn.
func (p *Page) statusFetchCmd() tea.Cmd {
	if p.fetcher == nil {
		return nil
	}
	fetcher := p.fetcher
	timeout := p.fetchTimeout
	// Parent on Options.FetchCtx when wired so app-level
	// cancellation propagates through the ctx (not only via
	// Close). nil falls back to context.Background() — kept so
	// tests / callers that don't pin a parent still work.
	parent := p.fetchCtx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, timeout)
		// Expose cancel to Close() so a page-tear-down while the
		// fetch is in flight aborts the call instead of leaking the
		// goroutine for the full fetchTimeout window.
		p.mu.Lock()
		p.cancelFetch = cancel
		p.mu.Unlock()
		defer func() {
			p.mu.Lock()
			p.cancelFetch = nil
			p.mu.Unlock()
			cancel()
		}()
		st, err := fetcher.Status(ctx)
		if err != nil {
			return statusFetchedMsg{err: err}
		}
		return statusFetchedMsg{cfg: st.Config}
	}
}

// Close implements app.Page. Cancels any in-flight Status fetch so
// the goroutine doesn't outlive the page; without this, navigating
// away from a slow-backend inspector strands one worker per close
// for the full fetchTimeout window (30 s by default).
func (p *Page) Close() tea.Cmd {
	p.mu.Lock()
	cancel := p.cancelFetch
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (*Page) Crumb() string { return "tenant-config" }

func (p *Page) Title() string {
	scope := p.tenant
	if scope == "" {
		scope = "—"
	}
	return "tenant-config(" + scope + ")"
}

func (p *Page) HeaderContent() string {
	if p.loading {
		return "fetching alertmanager config…"
	}
	return ""
}

// Bindings implements app.Page. The page is read-only; only
// scroll motions are surfaced to keep the hint strip sparse.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Esc", Description: "back", View: "tenant-config"},
	}
}

func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if m, ok := msg.(statusFetchedMsg); ok {
		p.loading = false
		if m.err != nil {
			if errors.Is(m.err, context.Canceled) {
				// ctx-cancel is shutdown noise, not a real fetch
				// failure. Drop silently rather than rendering
				// "(fetch failed: context canceled)" on the last frame.
				return p, nil
			}
			p.amErr = m.err
			return p, nil
		}
		p.amConfig = m.cfg
		return p, nil
	}
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	p.HandleScrollKey(keyMsg.String())
	return p, nil
}

// View implements app.Page. Builds two YAML-styled sections,
// scrolled by p.Scroll. Long files are rendered in full so the
// user can :%search-style scan; clamping happens at the bottom.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	p.BodyHeight = height
	lines := p.bodyLines()
	p.ReconcileScroll(len(lines), height)
	end := min(p.Scroll+height, len(lines))
	visible := lines[p.Scroll:end]
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
	out.URL = stripURLUserinfo(cfg.URL)
	out.ProxyURL = stripURLUserinfo(cfg.ProxyURL)
	out.BasicAuth = redactBasic(cfg.BasicAuth)
	out.Authorization = redactAuthorization(cfg.Authorization)
	if cfg.BearerToken != "" {
		out.BearerToken = redactionMarker
	}
	out.Headers = redactHeaders(cfg.Headers)
	// gosec G117 false positive: BearerToken (and BasicAuth /
	// Authorization / Headers) are replaced with redactionMarker
	// above before this Marshal — the inspector never emits real
	// credentials.
	body, err := yaml.Marshal(out) //nolint:gosec // G117: secrets replaced with redactionMarker above
	if err != nil {
		return "", fmt.Errorf("marshal backend: %w", err)
	}
	return string(body), nil
}

// stripURLUserinfo removes embedded credentials from a URL so the
// inspector never leaks them. A common shortcut for proxy or basic
// auth is to paste "https://user:pass@host" into the URL field;
// without stripping, both the username and password render in the
// redacted YAML at a glance. Unparseable inputs round-trip unchanged
// — the redactor must not make malformed config look more malformed.
func stripURLUserinfo(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
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

// partialRedact masks a non-secret-but-identifying field.
// BasicAuth.Username is identifying (not strictly secret) so emitting
// it unchanged in the tenantconfig inspector leaks the configured
// account name to over-the-shoulder observers. Keep the first two
// characters visible when the field is at least four characters long
// ("admin" -> "ad***", "prod-svc" -> "pr***") so the operator can
// still disambiguate between similarly-shaped configs (prod vs.
// staging vs. dev) without giving up the full identifier. Shorter
// strings fully redact — exposing two of two characters would be no
// redaction at all.
func partialRedact(s string) string {
	if s == "" {
		return ""
	}
	const exposeLen = 2
	const minLen = 4
	// Rune-aware: byte slicing splits multi-byte runes (e.g. CJK,
	// emoji, accented chars) mid-codepoint, producing invalid UTF-8
	// that renders as the replacement character or corrupts the
	// output. minLen and exposeLen are counted in display characters
	// for the same reason.
	runes := []rune(s)
	if len(runes) < minLen {
		return redactionMarker
	}
	return string(runes[:exposeLen]) + redactionMarker
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
