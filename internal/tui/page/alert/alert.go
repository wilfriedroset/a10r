// SPDX-License-Identifier: Apache-2.0

// Package alert renders the alert-detail page — a read-only view
// of one cached backend.Alert pushed from the alerts list row.
// There is no extra GET on push: the alerts list snapshot is
// sufficient and a poll tick will refresh it on its own schedule.
package alert

import (
	"bytes"
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
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
	silencepage "github.com/wilfriedroset/a10r/internal/tui/page/silence"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
	"github.com/wilfriedroset/a10r/internal/tui/yamlstyle"
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
	Styles *theme.Styles
	// Clipboard handles the `c` (copy fingerprint) action. nil
	// disables the binding gracefully — `c` flashes a "no clipboard
	// integration" hint instead of crashing. Was wired to `y` pre-G5;
	// `y` now owns the raw-YAML toggle.
	Clipboard Clipboard
	// Browser handles the `o` (open generatorURL) action. nil
	// disables the binding the same way.
	Browser Browser
	// Now injects the clock used by the age line. nil falls back
	// to time.Now.
	Now func() time.Time
	// Clients is the per-tenant write surface for `s`. Picked up
	// by tenant tag (this page knows its source backend); empty /
	// missing tenant flashes a hint instead of pushing a broken
	// form. Same shape the alerts list / silences page consume.
	Clients map[string]silenceform.Client
	// Creator seeds the silence form's CreatedBy field; usually
	// $USER. Empty falls back to "a10r" in the form factory.
	Creator string
	// TimeFormat seeds the page's time-format mode at push time
	// so the detail body opens in the same mode the parent list
	// page was already showing.
	TimeFormat timerender.Format
	// ReadOnly hides the page's Dangerous bindings (`s`) from the
	// hint strip / help overlay and turns the keystroke into a
	// flash hint. Wired from defaults.read_only / --read-only.
	ReadOnly bool
}

// Page is the alert-detail view. Implements app.Page.
type Page struct {
	*detailpage.Base

	a backend.Alert
	// silencedBy is the de-duplicated, order-preserving SilencedBy
	// list. Stored separately from a.SilencedBy so a non-conforming
	// upstream that emits the same ID twice cannot make the body
	// (which walks this list) and the picker (which resolves IDs
	// for `S`) disagree on how many distinct silences exist —
	// dedup at one boundary keeps both renderers in lockstep
	// without mutating the cached Alert.
	silencedBy []string
	tenant     string
	styles     *theme.Styles
	clip       Clipboard
	browser    Browser
	now        func() time.Time

	// clients is the per-tenant write surface for `s`. See Options.
	clients map[string]silenceform.Client
	// creator seeds the silence form's CreatedBy field.
	creator string

	// timeFormat mirrors the app-global toggle. Flipped by
	// app.TimeFormatChangedMsg so the summary's "age:" line reads
	// the same shape as the alerts list it was pushed from.
	timeFormat timerender.Format

	// silences caches the polled snapshot for p.tenant only, keyed
	// by silence ID. Populated from poll.DataMsg{ResourceLabel:
	// "silences"} arriving via the App's cache replay (on push) or
	// a live tick. Per-tenant filtering happens at ingest so the
	// per-page state stays minimal — silenced-by IDs in
	// backend.Alert are not cross-tenant.
	silences map[string]backend.Silence

	// readOnly mirrors Options.ReadOnly. Bindings() filters
	// Dangerous entries when set; the `s` handler flashes a hint
	// instead of pushing the silence form.
	readOnly bool

	// rawYAML toggles the body between the structured render
	// (default) and a raw alert payload dumped via output.WriteYAML.
	// k9s-style escape hatch for "what does the API actually
	// return?" debugging. The toggle is per-page (does not persist
	// across pushes) so a fresh drill-in always opens structured —
	// the structured view is the more legible default for triage.
	rawYAML bool
}

// New constructs an alert-detail page.
func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	p := &Page{
		Base:       &detailpage.Base{},
		a:          opts.Alert,
		silencedBy: dedupStrings(opts.Alert.SilencedBy),
		tenant:     opts.Tenant,
		styles:     opts.Styles,
		clip:       opts.Clipboard,
		browser:    opts.Browser,
		now:        now,
		clients:    opts.Clients,
		creator:    opts.Creator,
		timeFormat: opts.TimeFormat,
		silences:   map[string]backend.Silence{},
		readOnly:   opts.ReadOnly,
	}
	p.SetTimeFormat = func(f timerender.Format) { p.timeFormat = f }
	p.OnModalResult = p.handleModalResult
	return p
}

// PollResources implements app.PollAwarePage. The detail page only
// reacts to the silences feed — silenced-by UUIDs in the alert are
// resolved against this snapshot to enrich the suppression block.
// Listing the label here lets the App's cache replay hydrate the
// page on push so a freshly-drilled detail view shows enriched
// rows immediately, without waiting for the next poll tick.
func (*Page) PollResources() []string { return []string{"silences"} }

func (*Page) Crumb() string { return "detail" }

// Title implements app.Page — "Describe(<scope>/<alertname>)"
// mirrors the k9s pod-detail header. Appends ` [raw yaml]` when the
// page is in the `y`-toggled raw mode so the operator can tell at a
// glance which view they're looking at — the QA-driven G5 nit: the
// two modes rendered identically apart from the body, leaving the
// user with no signal which one was active.
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

// Bindings implements app.Page. When the page is in read-only
// mode the Dangerous entries (`s`) are stripped before the slice
// is returned so the hint strip and help overlay both render the
// read-only verb set.
//
// `y` toggles the raw-YAML escape hatch (k9s convention); `c`
// owns copy-to-clipboard. `y` was historically copy-fp but moved
// to `c` so the YAML toggle could land on the canonical key.
func (p *Page) Bindings() []action.Action {
	out := []action.Action{
		{Key: "s", Description: "silence", View: "alert", Dangerous: true},
		{Key: "S", Description: "open silence", View: "alert"},
		{Key: "y", Description: "yaml", View: "alert"},
		{Key: "c", Description: "copy fp", View: "alert"},
		{Key: "o", Description: "open URL", View: "alert"},
	}
	if p.readOnly {
		return action.FilterDangerous(out)
	}
	return out
}

// Update implements app.Page. Esc is intentionally NOT handled
// here — the App's global LayerGlobal Esc binding pops the stack
// (#23), which is exactly the right behaviour for a detail page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.DataMsg:
		p.ingestSilences(m)
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped; flash the new silence ID so the user
		// sees confirmation. Same shape the alerts list / silences
		// page use.
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
		// Toggle the raw-YAML body. Resetting scroll keeps the
		// switch unsurprising — a long structured view scrolled
		// halfway down would otherwise land the user mid-document
		// in a YAML payload of a different length.
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

// handleModalResult is the OnModalResult callback wired into
// Base.OnModalResult at construction. The silence-picker modal
// returns SilenceSelectedMsg / SilenceCancelledMsg via the typed
// wrapper in silencepicker.go; everything else falls through to
// the page's main switch (handled=false) so a sibling page's modal
// result that incidentally reached us cannot be silently swallowed.
func (p *Page) handleModalResult(m modal.ResultMsg) (bool, tea.Cmd) {
	switch v := m.(type) {
	case SilenceSelectedMsg:
		return true, p.openSilenceDetail(v.ID)
	case SilenceCancelledMsg:
		return true, nil
	}
	return false, nil
}

// ingestSilences caches the silences poll snapshot for p.tenant.
// Out-of-resource and out-of-tenant payloads are ignored — the
// suppression renderer only resolves IDs that came from the
// alert's own backend, and dropping the rest keeps the page's
// state proportional to one tenant's silence count rather than the
// whole multi-tenant fan-out.
func (p *Page) ingestSilences(m poll.DataMsg) {
	if m.ResourceLabel != "silences" || m.Tenant != p.tenant {
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

// openSilenceForm pushes the silence form prefilled with this
// alert's labels via silenceform.MatchersFromLabels (which drops
// the synthetic `__name__` key). Empty / unknown tenant flashes
// a hint instead of crashing — matches the alerts list `s` UX
// so the affordance reads consistently across pages.
func (p *Page) openSilenceForm() tea.Cmd {
	if len(p.clients) == 0 || p.tenant == "" {
		return footer.ShowFlash(footer.FlashWarn, hintNoWriteableBackend)
	}
	if _, ok := p.clients[p.tenant]; !ok {
		return footer.ShowFlash(footer.FlashWarn, hintNoWriteableBackend)
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

// hintNoWriteableBackend mirrors the alerts page's const so a
// wording tweak there is the only edit required to keep the
// affordance consistent. Two copies — one per package — beats a
// shared internal/tui/footer string when the only consumers are
// these two pages.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"

// hintReadOnly is the flash text emitted when `s` fires on a
// read-only alert detail page.
const hintReadOnly = "read-only mode — alerts cannot be silenced"

// copyFingerprint returns the Cmd that asks the clipboard
// integration to copy this alert's fingerprint, surfacing success
// or failure as a flash. nil clipboard is a graceful "no
// integration" path.
func (p *Page) copyFingerprint() tea.Cmd {
	if p.clip == nil {
		return footer.ShowFlash(footer.FlashWarn, "clipboard not configured")
	}
	if p.a.Fingerprint == "" {
		return footer.ShowFlash(footer.FlashWarn, "alert has no fingerprint")
	}
	if err := p.clip.Copy(p.a.Fingerprint); err != nil {
		return footer.ShowFlash(footer.FlashError, "copy failed: "+err.Error())
	}
	return footer.ShowFlash(footer.FlashSuccess, "fingerprint copied")
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
	if p.browser == nil {
		return footer.ShowFlash(footer.FlashWarn, "browser not configured")
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

// View implements app.Page. Builds a flat line list, hanging-
// indent-wraps any line that overflows width, then slices the
// visible window starting at p.Scroll.
//
// The rawYAML toggle swaps the line list for a raw-payload dump
// produced by output.WriteYAML; the scroll / styling / clamping
// machinery is untouched so the two modes share one viewport.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	p.BodyHeight = height
	var lines []string
	if p.rawYAML {
		lines = p.rawYAMLLines()
	} else {
		lines = p.bodyLines(width)
	}
	p.ReconcileScroll(len(lines), height)
	end := min(p.Scroll+height, len(lines))
	if p.Scroll > end {
		p.Scroll = end
	}
	visible := lines[p.Scroll:end]
	// Apply skin's YAML.Key / .Value / .Punct foreground roles per
	// line. yamlstyle short-circuits anything that doesn't look
	// like a real `key: value` row — comment-only lines, blank
	// separators, "(none)" placeholders, and (crucially) wrap
	// continuations + \n-split annotation segments whose pre-`:`
	// portion contains brackets/equals. Width-clamp matches the
	// other list / detail pages so the bordered body never sees
	// jagged-width content.
	for i, line := range visible {
		visible[i] = yamlstyle.Line(line, p.styles)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(visible, "\n"))
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
	if p.a.State == backend.AlertStateSuppressed {
		out = append(out, "", "Suppression:")
		out = append(out, p.suppressionLines(width)...)
	}
	return out
}

// alertYAML is the shape rendered when the user toggles raw mode
// with `y`. Field order and key names mirror the Alertmanager v2
// /api/v2/alerts wire payload so the body reads close to what the
// API actually returns — that's the whole point of the escape
// hatch. Empty optional
// collections elide via `omitempty` so a non-suppressed alert
// doesn't carry empty silencedBy / inhibitedBy / mutedBy noise.
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

// rawYAMLLines marshals the alert as raw-mode YAML and returns it
// as a flat line slice, ready for the same scroll / style pipeline
// the structured render uses. Marshal failures surface inline as
// a single descriptive line — the page never blanks; the user can
// still toggle back to structured. WriteYAML drives the encoder so
// indent / dep choice stays consistent with the headless `--output=yaml`
// path (silence detail uses yaml.Marshal directly because it
// pre-marshals once at construction; the alert page re-marshals on
// every View — cheap given the payload size — so the live
// app-global TimeFormat-toggled timestamp on the alert can flow
// through if a future revision threads it).
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

// suppressionLines renders the silenced-by / inhibited-by /
// muted-by lists for a suppressed alert. The silenced-by section
// resolves UUIDs against the polled silences snapshot to surface
// expiry / createdBy / comment alongside each ID, so the user can
// triage without round-tripping to the silences page.
// Inhibited-by and muted-by remain raw lists per the original
// shape — fingerprint resolution and time-interval enrichment are
// intentional non-goals here. The fixed section order
// (silenced → inhibited → muted) is preserved so the same
// suppressed alert renders identically across refreshes.
//
// Defensive empty-state: a suppressed alert with all three lists
// empty shouldn't happen against vanilla Alertmanager, but a
// non-conforming proxy or upstream bug could surface it. Render
// `(no reason reported by Alertmanager)` so the section header
// has at least one line under it instead of looking like a
// render glitch.
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

// silenceRowIndent is the leading indent for each silenced-by row
// under the "  silenced by:" sub-header. Two cols past the section
// indent so the rows visually nest under their header.
const silenceRowIndent = "    "

// silencedByRow renders one row for a silenced-by ID. Cache hit
// produces the dense single-line summary; cache miss produces the
// degraded marker row. The comment is clipped (width-aware) so a
// long incident note never wraps the row and breaks the column
// alignment of subsequent rows in the block.
//
// Whitespace-only and effectively-empty comments drop the "— "
// separator so the row never reads with a dangling em-dash, and
// terminals so narrow that the prefix already fills the width
// also drop the separator (rather than render "— " followed by
// nothing).
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

// silencePickerLine renders the unclipped per-silence summary used
// by the disambiguation modal. The modal's own width handling
// truncates if needed; we deliberately don't clip here so the
// picker shows the full comment when there's room. Whitespace-
// only comments drop the "  — " separator for the same reason
// silencedByRow does — a row that ends in a dangling em-dash
// reads as a render bug.
func (p *Page) silencePickerLine(id string) string {
	s, ok := p.silences[id]
	if !ok {
		return id + "  (silence not in snapshot)"
	}
	line := id + "  " + p.expiryField(s.EndsAt) + "  by " + s.CreatedBy
	comment := strings.TrimSpace(collapseFirstLine(s.Comment))
	if comment != "" {
		line += "  — " + comment
	}
	return line
}

// expiryField renders the "<label> <value>" middle column. Label
// flips with the app-global TimeFormat — "expires in" reads as a
// duration, "ends" reads as a wall-clock — so the row stays
// semantically honest in either mode and matches the summary-block
// pattern at renderSummary's age/started flip. Past-case `expired`
// is an alert-domain UX label absorbed here so timerender.Remaining
// stays strictly forward-looking per CONTEXT.md.
func (p *Page) expiryField(ts time.Time) string {
	if p.timeFormat == timerender.Absolute {
		return "ends " + timerender.Display(timerender.Absolute, p.now(), ts)
	}
	if ts.Sub(p.now()) <= 0 {
		return "expired"
	}
	return "expires in " + timerender.Remaining(p.now(), ts)
}

// clipComment truncates s so it fits within budget columns (never
// exceeds — the multiline branch used to overflow by one column at
// the boundary), appending an ellipsis ("…", one column) whenever
// content was hidden. A multiline comment ALWAYS ends in "…" even
// when the first line fits, because the user otherwise has no
// signal that more text exists below the rendered row.
//
// budget ≤ 0 returns "" so the caller can drop the row's "— "
// separator and avoid a dangling em-dash; budget 1 returns just
// the ellipsis, since we can't fit even one comment rune plus the
// marker.
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

// collapseFirstLine returns the substring of s up to the first
// newline. Used by the picker line where width clipping is the
// modal's responsibility but the multi-line collapse is still ours
// (literal "\n" runs would render as box-drawing artefacts inside
// the picker's plain-text body).
func collapseFirstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}

// openSilencedByDetail handles the `S` binding. No silenced-by
// entries (defensive: an active alert with `S` pressed, or a
// non-conforming proxy) flashes a soft Info hint so the binding
// reads as "no-op with explanation" rather than dead. One entry
// drills directly into silence detail; many open the typed-wrapper
// modal so the user picks one. Cache miss on direct push falls
// through to a flash via openSilenceDetail. The list walked here
// is the constructor-deduped p.silencedBy so the picker matches
// what the body shows.
func (p *Page) openSilencedByDetail() tea.Cmd {
	if len(p.silencedBy) == 0 {
		return footer.ShowFlash(footer.FlashInfo, "no silences attached to this alert")
	}
	if len(p.silencedBy) == 1 {
		return p.openSilenceDetail(p.silencedBy[0])
	}
	rows := make([]silencePickerRow, 0, len(p.silencedBy))
	for _, id := range p.silencedBy {
		rows = append(rows, silencePickerRow{
			id:   id,
			line: p.silencePickerLine(id),
		})
	}
	return app.OpenModal(func() modal.Modal {
		return newSilencePicker(rows)
	})
}

// dedupStrings preserves first-occurrence order. Pulled out so the
// `S` binding's ingest matches whatever ordering choice we made
// elsewhere — first-seen-first matches the stable section order
// already enforced for silenced/inhibited/muted in the body.
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

// openSilenceDetail pushes the silence detail page for the given
// ID, sourced from the polled snapshot. Cache miss flashes an
// Info hint pointing the user at `:silences` — the rendered
// degraded row in the body is space-constrained and only carries
// "(silence not in snapshot)", so the recovery path lives in this
// flash, fired the moment the user actually presses `S`.
//
// Note on TimeFormat: silence detail (internal/tui/page/silence)
// renders RFC3339 timestamps unconditionally and does not honour
// the app-global TimeFormat toggle, so there's nothing to forward
// here. If that page ever grows a relative-mode renderer, thread
// p.timeFormat through silencepage.Options at that time.
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
	if stamp := p.formatTime(p.a.StartsAt); stamp != "" {
		// "age" reads as a duration; in absolute mode the line
		// shows a wall-clock, so the label flips to "started" to
		// stay semantically honest. Same column width so the
		// values column doesn't shift on toggle.
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

// formatTime renders ts according to the page's active time
// format. Mirrors the alerts / silences formatters so the three
// views agree on how the toggle reads.
func (p *Page) formatTime(ts time.Time) string {
	return timerender.Display(p.timeFormat, p.now(), ts)
}
