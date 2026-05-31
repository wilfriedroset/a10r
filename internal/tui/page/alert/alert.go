// SPDX-License-Identifier: Apache-2.0

// Package alert renders a read-only detail view of one cached
// backend.Alert pushed from the alerts list; no GET on push, poll
// refreshes it.
package alert

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/output"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/browser"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	silencepage "github.com/wilfriedroset/a10r/internal/tui/page/silence"
	silencespage "github.com/wilfriedroset/a10r/internal/tui/page/silences"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
	"github.com/wilfriedroset/a10r/internal/tui/yamlstyle"
)

// Clipboard is the copy-to-clipboard seam. The Cmd runs in the
// bubbletea loop because OSC52 must go through the renderer, not a
// raw stdout write; it is fire-and-forget, so no failure to report.
type Clipboard interface {
	Copy(s string) tea.Cmd
}

// Browser is the open-URL seam; errors surface as flash messages.
type Browser interface {
	Open(url string) error
}

// osc52Clipboard is the default Clipboard, using the terminal's
// OSC52 sequence so it works over SSH and without an X/Wayland display.
type osc52Clipboard struct{}

func (osc52Clipboard) Copy(s string) tea.Cmd { return tea.SetClipboard(s) }

// Options bundles the per-page dependencies. The fields forwarded to
// the silences page pushed by `S` mirror silences.Options of the same name.
type Options struct {
	Alert  backend.Alert
	Tenant string
	Styles *theme.Styles
	// Clipboard handles `c` (copy fingerprint); nil defaults to OSC52.
	Clipboard Clipboard
	// Browser handles `o` (open generatorURL); nil defaults to the
	// platform launcher (xdg-open / open / start).
	Browser Browser
	// Now is the clock for the age line; nil falls back to time.Now.
	Now func() time.Time
	// Clients is the per-tenant write surface for `s`; empty /
	// missing tenant flashes a hint instead of pushing a broken form.
	Clients map[string]silenceform.Client
	// Creator seeds the silence form's CreatedBy field, usually
	// $USER; empty falls back to "a10r" in the form factory.
	Creator string
	// TimeFormat seeds the page's time-format mode at push so the
	// detail body opens in the same mode the parent list was showing.
	TimeFormat timerender.Format
	// ReadOnly hides Dangerous bindings (`s`) from the hint strip /
	// help overlay and turns the keystroke into a flash hint.
	ReadOnly bool
	// EditorResolver handles the `Ctrl+E` round-trip on the restricted
	// silences page pushed by `S` (N>1). Zero value flashes a hint.
	EditorResolver edit.Resolver
	// EditorCtx is the parent ctx the editor subprocess and bulk-expire
	// fanout inherit; nil falls back to context.Background().
	EditorCtx context.Context //nolint:containedctx // editor subprocess ctx, plumbed once at construction.
	// BulkConcurrency caps the per-tenant bulk worker pool; zero resolves
	// to the config default inside silences.New.
	BulkConcurrency int
	// Logger receives per-failure detail from bulk operations; nil suppresses logging.
	Logger *slog.Logger
	// BulkCtx is the parent ctx the bulk-expire fanout inherits; nil falls back to context.Background().
	BulkCtx context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.
	// SubmitCtx is the parent ctx the silence form's submit ctx derives from; nil falls back to context.Background().
	SubmitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
}

const (
	resourceSilences = "silences"
	viewAlert        = "alert"
	silenceExpired   = "expired"
)

type Page struct {
	*detailpage.Base

	a backend.Alert
	// silencedBy is the de-duplicated, order-preserving SilencedBy list,
	// stored separately from a.SilencedBy so a non-conforming upstream
	// emitting an ID twice can't make the body and the `S` picker
	// disagree on the count — dedup at one boundary, no mutation of the cached Alert.
	silencedBy []string
	tenant     string
	styles     *theme.Styles
	clip       Clipboard
	browser    Browser
	now        func() time.Time

	clients map[string]silenceform.Client
	creator string

	// timeFormat is flipped by app.TimeFormatChangedMsg so the detail reads the same shape as the list.
	timeFormat timerender.Format

	// silences caches the polled snapshot for p.tenant only, keyed by ID.
	// Filtering to one tenant at ingest keeps state minimal; silenced-by
	// IDs in backend.Alert are never cross-tenant.
	silences map[string]backend.Silence

	// readOnly filters Dangerous bindings and turns `s` into a flash hint.
	readOnly bool

	// rawYAML toggles the body to a raw payload dump (k9s-style escape
	// hatch). Per-page, not persisted across pushes, so a fresh drill-in
	// always opens on the more legible structured view.
	rawYAML bool

	// These fields are forwarded to the restricted silences page pushed
	// by `S` when the alert has N>1 silenced-by IDs (ADR 0035).
	editorResolver  edit.Resolver
	editorCtx       context.Context //nolint:containedctx // editor subprocess ctx, plumbed once at construction.
	bulkConcurrency int
	logger          *slog.Logger
	bulkCtx         context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.
	submitCtx       context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
}

func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	clip := opts.Clipboard
	if clip == nil {
		clip = osc52Clipboard{}
	}
	br := opts.Browser
	if br == nil {
		br = browser.System{}
	}
	p := &Page{
		Base:            &detailpage.Base{},
		a:               opts.Alert,
		silencedBy:      dedupStrings(opts.Alert.SilencedBy),
		tenant:          opts.Tenant,
		styles:          opts.Styles,
		clip:            clip,
		browser:         br,
		now:             now,
		clients:         opts.Clients,
		creator:         opts.Creator,
		timeFormat:      opts.TimeFormat,
		silences:        map[string]backend.Silence{},
		readOnly:        opts.ReadOnly,
		editorResolver:  opts.EditorResolver,
		editorCtx:       opts.EditorCtx,
		bulkConcurrency: opts.BulkConcurrency,
		logger:          opts.Logger,
		bulkCtx:         opts.BulkCtx,
		submitCtx:       opts.SubmitCtx,
	}
	p.SetTimeFormat = func(f timerender.Format) { p.timeFormat = f }
	return p
}

// PollResources implements app.PollAwarePage.
func (*Page) PollResources() []string { return []string{resourceSilences} }

func (*Page) Crumb() string { return "detail" }

func (p *Page) Title() string {
	scope := p.tenant
	if scope == "" {
		scope = "—"
	}
	base := "Describe(" + scope + "/" + p.a.Labels["alertname"] + ")"
	if p.rawYAML {
		base += " [raw yaml]"
	}
	return base
}

// Bindings returns the page's key bindings; Dangerous (`s`) entries
// are stripped in read-only mode.
func (p *Page) Bindings() []action.Action {
	out := []action.Action{
		{Key: "s", Description: "silence", View: viewAlert, Dangerous: true},
		{Key: "S", Description: "open silences", View: viewAlert},
		{Key: "y", Description: "yaml", View: viewAlert},
		{Key: "c", Description: "copy fp", View: viewAlert},
		{Key: "o", Description: "open URL", View: viewAlert},
	}
	if p.readOnly {
		return action.FilterDangerous(out)
	}
	return out
}

// Update implements app.Page. Esc is intentionally NOT handled here —
// the App's global LayerGlobal Esc binding pops the stack, the right
// behaviour for a detail page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.DataMsg:
		p.ingestSilences(m)
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped; flash the new ID for confirmation, same
		// shape the alerts list / silences page use.
		slog.Default().Info("silence write succeeded",
			slog.String("op", "created"),
			slog.String("id", m.ID),
			slog.String("surface", "alert-detail-form"))
		return p, footer.ShowFlash(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. No flash — Esc is a non-event.
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	key := keyMsg.String()
	switch key {
	case "y":
		// Reset scroll on toggle: a half-scrolled structured view
		// would otherwise land the user mid-document in a YAML payload
		// of a different length.
		p.rawYAML = !p.rawYAML
		p.Scroll = 0
		return p, nil
	case "c":
		cmd := p.copyFingerprint()
		return p, cmd
	case "o":
		cmd := p.openGeneratorURL()
		return p, cmd
	case "s":
		if p.readOnly {
			return p, footer.ShowFlash(footer.FlashWarn, hintReadOnly)
		}
		cmd := p.openSilenceForm()
		return p, cmd
	case "S":
		cmd := p.openSilencedByDetail()
		return p, cmd
	}
	p.HandleScrollKey(key)
	return p, nil
}

// ingestSilences caches the silences poll snapshot for p.tenant.
// Out-of-resource and out-of-tenant payloads are dropped so state
// stays proportional to one tenant rather than the whole fan-out.
func (p *Page) ingestSilences(m poll.DataMsg) {
	if m.ResourceLabel != resourceSilences || m.Tenant != p.tenant {
		return
	}
	sils, ok := m.Resource.([]backend.Silence)
	if !ok {
		return
	}
	next := make(map[string]backend.Silence, len(sils))
	for _, s := range sils {
		next[s.ID] = s
	}
	p.silences = next
}

// openSilenceForm pushes the silence form; an empty or unknown tenant
// flashes a hint instead, matching the alerts list `s` UX.
func (p *Page) openSilenceForm() tea.Cmd {
	if len(p.clients) == 0 || p.tenant == "" {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
	}
	if _, ok := p.clients[p.tenant]; !ok {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
	}
	matchers := silenceform.MatchersFromLabels(p.a.Labels)
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	clients := p.clients
	tenant := p.tenant
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:  clients,
			Tenant:   tenant,
			Styles:   styles,
			Now:      now,
			Creator:  creator,
			Matchers: matchers,
		})
	})
}

const hintReadOnly = "read-only mode — alerts cannot be silenced"

func (p *Page) copyFingerprint() tea.Cmd {
	if p.a.Fingerprint == "" {
		return footer.ShowFlash(footer.FlashWarn, "alert has no fingerprint")
	}
	return tea.Batch(
		p.clip.Copy(p.a.Fingerprint),
		footer.ShowFlash(footer.FlashSuccess, "fingerprint copied"),
	)
}

// openGeneratorURL asks the browser integration to open the
// alert's generatorURL. Missing URL is a soft no-op with a hint
// (alerts without a generator URL are entirely valid per the AM
// schema, just less linkable). Schemes other than http(s) are
// refused: a malicious upstream (or compromised relabel config)
// can stamp javascript:/file:/data: URLs onto an alert and the OS
// handler could execute arbitrary code or read arbitrary files —
// the browser is the only sensible target for an alert link.
func (p *Page) openGeneratorURL() tea.Cmd {
	if p.a.GeneratorURL == "" {
		return footer.ShowFlash(footer.FlashInfo, "this alert has no generator URL")
	}
	if !isSafeBrowserURL(p.a.GeneratorURL) {
		return footer.ShowFlash(footer.FlashError, "refusing to open non-http(s) URL")
	}
	if err := p.browser.Open(p.a.GeneratorURL); err != nil {
		return footer.ShowFlash(footer.FlashError, "open failed: "+err.Error())
	}
	return footer.ShowFlash(footer.FlashSuccess, "opened in browser")
}

// isSafeBrowserURL accepts http(s) URLs only. Anything else is
// refused before reaching the OS handler so a hostile generatorURL
// can't be turned into a code-execution or file-read primitive via
// xdg-open / open / start.
func isSafeBrowserURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	}
	return false
}

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	var lines []string
	if p.rawYAML {
		lines = p.rawYAMLLines()
	} else {
		lines = p.bodyLines(width)
	}
	visible := p.Visible(lines, height)
	// yamlstyle applies the skin's Key/Value/Punct roles and
	// short-circuits non-`key: value` lines (blanks, "(none)", wrap
	// continuations, bracketed annotation segments).
	for i, line := range visible {
		visible[i] = yamlstyle.Line(line, p.styles)
	}
	return listpage.Wrap(width, strings.Join(visible, "\n"))
}

// bodyLines builds the structured line list View slices, so the scroll
// clamp has an exact length.
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
	if p.a.State == backend.AlertStateSuppressed {
		out = append(out, "", "Suppression:")
		out = append(out, p.suppressionLines(width)...)
	}
	return out
}

// alertYAML is the raw-mode (`y`) shape. Field order and key names
// mirror the Alertmanager v2 /api/v2/alerts wire payload so the body
// reads close to what the API returns; omitempty elides empty optional
// collections so a non-suppressed alert carries no silencedBy noise.
type alertYAML struct {
	Fingerprint  string             `yaml:"fingerprint,omitempty"`
	State        backend.AlertState `yaml:"state"`
	StartsAt     string             `yaml:"startsAt,omitempty"`
	EndsAt       string             `yaml:"endsAt,omitempty"`
	GeneratorURL string             `yaml:"generatorURL,omitempty"`
	Labels       map[string]string  `yaml:"labels"`
	Annotations  map[string]string  `yaml:"annotations,omitempty"`
	SilencedBy   []string           `yaml:"silencedBy,omitempty"`
	InhibitedBy  []string           `yaml:"inhibitedBy,omitempty"`
	MutedBy      []string           `yaml:"mutedBy,omitempty"`
	Receivers    []string           `yaml:"receivers,omitempty"`
}

// rawYAMLLines marshals the alert as raw-mode YAML into a flat line
// slice. Marshal failure surfaces as one descriptive line so the page
// never blanks; WriteYAML matches the headless `--output=yaml` path.
func (p *Page) rawYAMLLines() []string {
	doc := alertYAML{
		Fingerprint:  p.a.Fingerprint,
		State:        p.a.State,
		GeneratorURL: p.a.GeneratorURL,
		Labels:       p.a.Labels,
		Annotations:  p.a.Annotations,
		SilencedBy:   p.silencedBy,
		InhibitedBy:  p.a.InhibitedBy,
		MutedBy:      p.a.MutedBy,
		Receivers:    p.a.Receivers,
	}
	if !p.a.StartsAt.IsZero() {
		doc.StartsAt = p.a.StartsAt.UTC().Format(time.RFC3339)
	}
	if !p.a.EndsAt.IsZero() {
		doc.EndsAt = p.a.EndsAt.UTC().Format(time.RFC3339)
	}
	var buf bytes.Buffer
	if err := output.WriteYAML(&buf, doc); err != nil {
		return []string{fmt.Sprintf("(failed to render raw yaml: %v)", err)}
	}
	body := strings.TrimRight(buf.String(), "\n")
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// suppressionLines renders the silenced/inhibited/muted lists for a
// suppressed alert. Silenced-by IDs resolve against the polled snapshot
// to surface expiry/createdBy/comment for in-place triage; inhibited-by
// and muted-by stay raw (enrichment is an intentional non-goal). The
// fixed section order is preserved so refreshes render identically.
//
// The empty-state line guards a non-conforming upstream: all three lists
// empty shouldn't happen against vanilla Alertmanager, but a placeholder
// keeps the section from looking like a render glitch.
func (p *Page) suppressionLines(width int) []string {
	out := make([]string, 0, 8)
	if len(p.silencedBy) > 0 {
		out = append(out, "  silenced by:")
		for _, id := range p.silencedBy {
			out = append(out, p.silencedByRow(id, width))
		}
	}
	if len(p.a.InhibitedBy) > 0 {
		prefix := "  inhibited by: "
		hangCols := lipgloss.Width(prefix)
		out = append(out, wrapHanging(prefix+strings.Join(p.a.InhibitedBy, ", "), width, hangCols)...)
	}
	if len(p.a.MutedBy) > 0 {
		prefix := "  muted by:     "
		hangCols := lipgloss.Width(prefix)
		out = append(out, wrapHanging(prefix+strings.Join(p.a.MutedBy, ", "), width, hangCols)...)
	}
	if len(out) == 0 {
		return []string{"  (no reason reported by Alertmanager)"}
	}
	return out
}

// silenceRowIndent nests each silenced-by row two cols past its
// "  silenced by:" sub-header.
const silenceRowIndent = "    "

// silencedByRow renders one silenced-by row (cache miss yields a
// degraded marker). The comment is width-clipped so a long note never
// wraps and breaks column alignment of later rows; empty comments or a
// width-filling prefix drop the "— " separator to avoid a dangling em-dash.
func (p *Page) silencedByRow(id string, width int) string {
	s, ok := p.silences[id]
	if !ok {
		return silenceRowIndent + id + "  (silence not in snapshot)"
	}
	prefix := silenceRowIndent + id + "  " + p.expiryField(s.EndsAt) + "  by " + s.CreatedBy
	comment := strings.TrimSpace(s.Comment)
	if comment == "" {
		return prefix
	}
	const sep = "  — "
	clip := clipComment(comment, width-lipgloss.Width(prefix+sep))
	if clip == "" {
		return prefix
	}
	return prefix + sep + clip
}

// expiryField renders the middle column, flipping label with TimeFormat
// ("expires in" duration vs "ends" wall-clock) to stay semantically
// honest. The past-case `expired` label lives here so timerender.Remaining
// stays strictly forward-looking per CONTEXT.md.
func (p *Page) expiryField(ts time.Time) string {
	if p.timeFormat == timerender.Absolute {
		return "ends " + timerender.Display(timerender.Absolute, p.now(), ts)
	}
	if ts.Sub(p.now()) <= 0 {
		return silenceExpired
	}
	return "expires in " + timerender.Remaining(p.now(), ts)
}

// clipComment truncates s to at most budget columns, appending "…"
// when content was hidden. A multiline comment ALWAYS ends in "…" even
// if the first line fits, else the user has no signal that more text
// exists below the row. budget ≤ 0 returns "" so the caller can drop
// the "— " separator; budget 1 returns just the ellipsis.
func clipComment(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	first, _, multiline := strings.Cut(s, "\n")
	s = first
	width := lipgloss.Width(s)
	needsEllipsis := multiline || width > budget
	if !needsEllipsis {
		return s
	}
	if budget == 1 {
		return "…"
	}
	if width+1 <= budget {
		return s + "…"
	}
	cut := hardCutAt(s, budget-1)
	return s[:cut] + "…"
}

// openSilencedByDetail handles `S`: zero entries flash a hint, one
// pushes silence-detail, two-plus push the silences list restricted to
// the alert's silenced-by IDs (ADR 0035) with a silences(<alertname>) title.
func (p *Page) openSilencedByDetail() tea.Cmd {
	if len(p.silencedBy) == 0 {
		return footer.ShowFlash(footer.FlashInfo, "no silences attached to this alert")
	}
	if len(p.silencedBy) == 1 {
		return p.openSilenceDetail(p.silencedBy[0])
	}
	silencedBy := p.silencedBy
	styles := p.styles
	now := p.now
	clients := p.clients
	creator := p.creator
	editorResolver := p.editorResolver
	timeFormat := p.timeFormat
	bulkConcurrency := p.bulkConcurrency
	logger := p.logger
	readOnly := p.readOnly
	editorCtx := p.editorCtx
	bulkCtx := p.bulkCtx
	submitCtx := p.submitCtx
	tenant := p.tenant
	alertName := p.a.Labels["alertname"]
	labels := p.a.Labels
	return app.PushPage(func() app.Page {
		return silencespage.New(silencespage.Options{
			Styles:          styles,
			Now:             now,
			Clients:         clients,
			Creator:         creator,
			EditorResolver:  editorResolver,
			TimeFormat:      timeFormat,
			BulkConcurrency: bulkConcurrency,
			Logger:          logger,
			ReadOnly:        readOnly,
			EditorCtx:       editorCtx,
			BulkCtx:         bulkCtx,
			SubmitCtx:       submitCtx,
			Tenants:         []string{tenant},
			RestrictIDs:     silencedBy,
			AlertName:       alertName,
			AlertLabels:     labels,
		})
	})
}

// dedupStrings preserves first-occurrence order to match the stable
// silenced/inhibited/muted section order in the body.
func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// openSilenceDetail pushes silence detail for id from the polled
// snapshot. Cache miss flashes a `:silences` recovery hint, since the
// space-constrained degraded body row can't carry it.
//
// silence detail renders RFC3339 unconditionally and ignores the
// app-global TimeFormat, so nothing is forwarded; thread p.timeFormat
// through if that page ever grows a relative-mode renderer.
func (p *Page) openSilenceDetail(id string) tea.Cmd {
	s, ok := p.silences[id]
	if !ok {
		return footer.ShowFlash(footer.FlashInfo, "silence "+id+" not in snapshot — try :silences")
	}
	tenant := p.tenant
	styles := p.styles
	return app.PushPage(func() app.Page {
		return silencepage.New(silencepage.Options{
			Silence: s,
			Tenant:  tenant,
			Styles:  styles,
		})
	})
}

// splitLines splits s on \n so renderSummary's output joins with the
// section headers in bodyLines.
func splitLines(s string) []string { return strings.Split(s, "\n") }

// renderSummary is the top block, one field per line.
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
	if stamp := p.formatTime(p.a.StartsAt); stamp != "" {
		// Label flips age→started in absolute mode (duration vs
		// wall-clock); same width so the values column doesn't shift.
		label := "age:         "
		if p.timeFormat == timerender.Absolute {
			label = "started:     "
		}
		lines = append(lines, label+stamp)
	}
	if p.tenant != "" {
		lines = append(lines, "tenant:      "+p.tenant)
	}
	return strings.Join(lines, "\n")
}

// kvLines renders a map as sorted "  key: value" lines. Embedded "\n"
// in a value (common in Prometheus annotations) becomes its own row at
// the wrap-continuation hanging indent, so multi-line values align
// under the value column. Empty maps render as "  (none)".
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

// wrapHanging wraps s to width columns, indenting continuations by
// hangingCols. Word-wraps at whitespace, hard-cutting when a single
// word overflows — or, crucially, when the only whitespace sits inside
// the hanging indent, which would otherwise loop forever cutting only
// the indent and never the content.
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
		// Forward-progress guard: a cut at/before the indent yields a
		// no-content line that never shrinks rest, so hard-cut instead.
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

// hardCutAt returns the byte index where s's leading slice fits within
// limit columns; the forward-progress fallback when bestBreakIndex stalls.
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

// bestBreakIndex returns the byte index to split s so the leading slice
// fits within limit columns, preferring the last whitespace at-or-before
// the limit and hard-cutting when a single word overflows.
func bestBreakIndex(s string, limit int) int {
	if lipgloss.Width(s) <= limit {
		return len(s)
	}
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

// formatTime renders ts in the page's active time format, mirroring
// the alerts/silences formatters so the three views agree.
func (p *Page) formatTime(ts time.Time) string {
	return timerender.Display(p.timeFormat, p.now(), ts)
}
