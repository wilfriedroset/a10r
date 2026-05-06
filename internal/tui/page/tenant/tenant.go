// SPDX-License-Identifier: Apache-2.0

// Package tenant renders the tenant table per C3: one row per
// configured backend with NAME / URL / VERSION columns and
// connection / count metadata. As of #7 the table is read-only
// — Enter drills into the per-tenant config inspector
// (tenantconfig package); scope selection lives entirely on the
// global numeric quick-switch.
package tenant

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Row is one tenant's renderable state. The wiring layer rebuilds
// the slice on every redraw from the configured backends + the
// startup-fetched version map. Conn / Alerts / Silence are
// deliberately absent — the read-only inspector drops them rather
// than render zero-valued placeholders that the user would read
// as "every backend is connected with zero alerts" by accident
// (header.ConnState's zero value is ConnConnected). A future
// commit can re-attach those columns once the wiring layer
// publishes a per-(resource, tenant) snapshot map.
type Row struct {
	Name    string
	URL     string
	Version string
}

// Options bundles the constructor inputs.
type Options struct {
	// Styles is the compiled theme.
	Styles theme.Styles
	// DrillFactory builds the destination page when the user
	// presses Enter on a row. Returning a non-nil error makes the
	// page flash the message instead of pushing — useful when
	// the named backend is misconfigured (e.g. factory.Build
	// failed at startup so the inspector would render against a
	// nil fetcher). Required: nil DrillFactory makes Enter a
	// silent no-op the user has no way to debug.
	DrillFactory func(name string) (app.Page, error)
}

// Page is the tenant table view.
type Page struct {
	styles theme.Styles
	drill  func(name string) (app.Page, error)

	rows   []Row
	cursor int
	topRow int // first visible row; reconciled in View

	// bodyHeight is the row capacity (excluding the column-header
	// line) snapshotted on the most recent View. Ctrl+D / Ctrl+U
	// step half this; Ctrl+F / Ctrl+B step body-2 (vim's CTRL-F
	// two-line overlap convention). Zero before the first render —
	// handlers fall back to 10 / 20 so a keystroke that beats the
	// initial WindowSizeMsg still moves a sane distance.
	bodyHeight int

	// scope tracks the active tenant scope as observed from
	// app.ScopeChangedMsg — "all" includes every row; a single
	// name flags exactly that row as in-scope; comma-joined names
	// flag each one. The page does NOT mutate the scope itself
	// (the global numeric quick-switch owns that); it only mirrors
	// what the App announced so the user can see at a glance which
	// row is currently fanned-out.
	scope string
}

// New constructs a tenant page from Options.
func New(opts Options) *Page {
	return &Page{styles: opts.Styles, drill: opts.DrillFactory, scope: "all"}
}

// SetRows replaces the rendered rows. Used by the wiring layer
// instead of a poll.DataMsg path because tenant rows are derived
// from configuration + every (backend, resource) poller — there's
// no single DataMsg shape that fits.
func (p *Page) SetRows(rows []Row) {
	p.rows = rows
	if p.cursor >= len(rows) {
		p.cursor = max(len(rows)-1, 0)
	}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "tenant" }

// Title implements app.Page. Mirrors the rest of the list pages:
// `tenants(<scope>)[<count>]`. Scope is the active selection so
// the title carries the same scope label the user sees in the top
// panel.
func (p *Page) Title() string {
	scope := p.scope
	if scope == "" {
		scope = "all"
	}
	return fmt.Sprintf("tenants(%s)[%d]", scope, len(p.rows))
}

// HeaderContent implements app.Page. Tenant table is read-only
// as of #7; nothing live to surface in the subtitle line.
func (*Page) HeaderContent() string { return "" }

// Footer implements app.Page. Tenant table doesn't surface
// ambient state in the bottom border.
func (*Page) Footer() string { return "" }

// Bindings implements app.Page.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Enter", Description: "config", View: "tenant"},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case app.GoToFirstRowMsg:
		p.cursor = 0
		return p, nil
	case app.ScopeChangedMsg:
		// The App's LayerGlobal numeric quick-switch (`<0>` all,
		// `<1>`-`<9>` per backend) emits this. Mirroring it here
		// lets the table show the user which row is fanned-out
		// without forcing them to glance at the top panel.
		p.scope = m.Scope
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch keyMsg.String() {
	case "j", "down":
		if p.cursor < len(p.rows)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	// `g` alone is dead code — the dispatcher's chord buffer at
	// LayerTable consumes the first `g` waiting for the second. The
	// chord-completed `gg` arrives as app.GoToFirstRowMsg and is
	// handled in Update.
	case "G":
		p.cursor = max(len(p.rows)-1, 0)
	case "ctrl+d":
		p.cursor = min(p.cursor+p.halfPageStep(), max(len(p.rows)-1, 0))
	case "ctrl+u":
		p.cursor = max(p.cursor-p.halfPageStep(), 0)
	case "ctrl+f":
		p.cursor = min(p.cursor+p.fullPageStep(), max(len(p.rows)-1, 0))
	case "ctrl+b":
		p.cursor = max(p.cursor-p.fullPageStep(), 0)
	case "enter":
		cmd := p.drillToConfig()
		return p, cmd
	}
	// Numeric quick-switch (`0`, `1`-`9`) is owned by the App's
	// LayerGlobal binding (see app.registerTenantBindings) — it
	// emits ScopeChangedMsg so every page reacts the same way.
	// The tenant page therefore does NOT bind the digits locally;
	// the dispatcher consumes them before forwardToTop runs.
	return p, nil
}

// halfPageStep returns the Ctrl+D / Ctrl+U distance: half the
// rendered body height, with a 10-row cold-start fallback. Floored
// at 1 so a future narrowing of the cold-start guard cannot turn
// the binding into a no-op.
func (p *Page) halfPageStep() int {
	if p.bodyHeight < 2 {
		return 10
	}
	return max(p.bodyHeight/2, 1)
}

// fullPageStep returns the Ctrl+F / Ctrl+B distance: a full body
// minus two lines of context (vim's CTRL-F convention), with a
// 20-row cold-start fallback. Floored at 1 for the same reason as
// halfPageStep.
func (p *Page) fullPageStep() int {
	if p.bodyHeight < 4 {
		return 20
	}
	return max(p.bodyHeight-2, 1)
}

// drillToConfig pushes the tenantconfig page produced by the
// drill factory, or flashes the factory's error if the named
// backend is misconfigured. Reads from rowsSorted (the rendered
// order) so the drill matches the row the user sees under the
// cursor — the unsorted p.rows would silently disagree on every
// backend list whose insertion order isn't already alphabetical.
// nil factory or empty rows are silent no-ops; both are
// constructor configuration errors the user has no way to fix
// from inside this page.
func (p *Page) drillToConfig() tea.Cmd {
	if p.drill == nil {
		return nil
	}
	rows := p.rowsSorted()
	if p.cursor >= len(rows) {
		return nil
	}
	name := rows[p.cursor].Name
	page, err := p.drill(name)
	if err != nil {
		return flashFn(footer.FlashWarn, err.Error())
	}
	return app.PushPage(func() app.Page { return page })
}

// flashFn returns a Cmd emitting a FlashShowMsg with the supplied
// level and text. Mirror of the alerts / silences / groups
// helper so the wording stays consistent across pages.
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(p.rows) == 0 {
		// Render bg-less so the empty state matches the regular
		// table view's framing — both use the terminal default
		// background. styles.Body.Default would paint the body
		// palette behind the empty pane, which renders as a
		// coloured patch the populated view doesn't have, breaking
		// the visual parity between "loading" and "loaded" frames.
		return lipgloss.NewStyle().Width(width).Height(height).Render("no backends configured")
	}
	headerLine := p.renderHeader(width)
	bodyHeight := max(height-1, 0)
	p.bodyHeight = bodyHeight
	maxRows := min(bodyHeight, len(p.rows))
	p.reconcileScroll(maxRows)
	end := min(p.topRow+maxRows, len(p.rows))
	out := make([]string, 0, end-p.topRow+1)
	out = append(out, headerLine)
	rows := p.rowsSorted()
	canonical := canonicalDigits(rows)
	for i := p.topRow; i < end; i++ {
		row := rows[i]
		// Scope glyph indicates whether the row is part of the
		// active global scope (the numeric quick-switch state).
		// `●` reads at a glance against the row body.
		scopeGlyph := " "
		if p.scopeIncludes(row.Name) {
			scopeGlyph = "●"
		}
		// Canonical-digit annotation for the first 9 backends in
		// alphabetical order — the same order the global numeric
		// quick-switch <1>-<9> binds to. Lets the user read the
		// digit a backend is reachable by without counting rows.
		// Always 4 cols wide ("[N] " or "    ") so row alignment
		// is stable whether or not a digit is shown.
		digitGlyph := p.styles.Table.Dimmed.Render(canonical[row.Name])
		version := row.Version
		if version == "" {
			version = "—"
		}
		columns := []string{
			row.Name,
			row.URL,
			version,
		}
		prefix := "  "
		if i == p.cursor {
			prefix = "▸ "
		}
		body := digitGlyph + scopeGlyph + " " + p.padTenantColumns(columns, width)
		line := padRight(prefix+body, width)
		switch {
		case i == p.cursor:
			line = p.styles.Table.Cursor.Render(line)
		case p.scopeIncludes(row.Name):
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Marked.GetForeground()).
				Render(line)
		}
		out = append(out, line)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

// renderHeader returns the styled column-title row. Mirrors the
// alerts / silences pages' uppercased fg-only header.
func (p *Page) renderHeader(width int) string {
	titles := []string{"NAME", "URL", "VERSION"}
	// Match the per-row prefix ("▸ "/"  " + canonical digit "[N] "
	// + scope glyph + " ") so columns align with their headers.
	const prefix = "        "
	line := prefix + p.padTenantColumns(titles, width)
	return lipgloss.NewStyle().
		Foreground(p.styles.Table.Header.GetForeground()).
		Render(line)
}

// canonicalDigits returns a name → "[N] " glyph map for the first
// 9 backends in the supplied (alphabetical-by-name) row slice;
// rows past index 8 map to "    " so the per-row layout stays
// constant width. Computed once per render — small enough that a
// single allocation per redraw beats threading more state through
// the renderer.
func canonicalDigits(rows []Row) map[string]string {
	out := make(map[string]string, len(rows))
	for i, r := range rows {
		if i < 9 {
			out[r.Name] = "[" + strconv.Itoa(i+1) + "] "
		} else {
			out[r.Name] = "    "
		}
	}
	return out
}

// tenant column widths. URL gets the flex column since the visible
// host/port string is the most variable; the other two are fixed.
const (
	tenantColName    = 16
	tenantColVersion = 14
)

// padTenantColumns lays out a row across NAME / URL (flex) /
// VERSION columns at fixed widths with URL absorbing the
// remaining width.
func (p *Page) padTenantColumns(parts []string, width int) string {
	if len(parts) == 0 {
		return ""
	}
	const prefixCols = 8 // "▸ " + "[N] " + scope glyph + " "
	used := tenantColName + tenantColVersion + prefixCols
	flex := max(width-used, 16)
	cols := []int{tenantColName, flex, tenantColVersion}
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(padRight(v, cols[i]))
	}
	return b.String()
}

// scopeIncludes reports whether the named tenant is part of the
// active global scope. "all" / empty includes everyone; otherwise
// the scope is matched against the comma-joined name list (so the
// future Ctrl+T multi-select path "prod,staging" lights up both
// rows).
func (p *Page) scopeIncludes(name string) bool {
	scope := strings.TrimSpace(p.scope)
	if scope == "" || scope == "all" {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == name {
			return true
		}
	}
	return false
}

// reconcileScroll keeps p.cursor inside [topRow, topRow+maxRows).
func (p *Page) reconcileScroll(maxRows int) {
	if p.cursor < p.topRow {
		p.topRow = p.cursor
	}
	if p.cursor >= p.topRow+maxRows {
		p.topRow = p.cursor - maxRows + 1
	}
	maxTop := max(len(p.rows)-maxRows, 0)
	if p.topRow > maxTop {
		p.topRow = maxTop
	}
	if p.topRow < 0 {
		p.topRow = 0
	}
}

// padRight pads s with trailing spaces to w columns so the
// cursor's background extends across the whole row.
func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// rowsSorted returns the rows alphabetically by Name so the
// numeric quick-switch is stable across redraws.
func (p *Page) rowsSorted() []Row {
	out := make([]Row, len(p.rows))
	copy(out, p.rows)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
