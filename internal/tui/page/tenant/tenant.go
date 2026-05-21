// SPDX-License-Identifier: Apache-2.0

// Package tenant renders the tenant table: one row per
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
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Sort column keys. Stable identifiers handed to the tablesort
// helper. Default sort is NAME ASC so the visible order matches
// the canonical order on first paint — the digit annotations
// align with their visible row positions until the user actively
// sorts another way.
const (
	sortKeyName    = "name"
	sortKeyURL     = "url"
	sortKeyVersion = "version"
)

// tenantSortColumns returns the page's sortable columns. URL and
// VERSION default ASC for consistency with NAME; the version
// comparator is semver-aware so "0.27.0" sorts after "0.9.0"
// (the lexical mistake a default string compare would make is
// exactly what the operator scanning for stale backends needs to
// see right). Empty version strings sort LAST in ASC mode —
// "unknown" rather than "lowest" — so concrete versions surface
// at the top regardless of direction.
func tenantSortColumns() []tablesort.Column[Row] {
	return []tablesort.Column[Row]{
		{
			Key: sortKeyName, Title: "NAME", Hotkey: 'N', DefaultAsc: true,
			Less: func(a, b *Row) bool { return a.Name < b.Name },
		},
		{
			Key: sortKeyURL, Title: "URL", Hotkey: 'U', DefaultAsc: true,
			Less: func(a, b *Row) bool {
				if a.URL != b.URL {
					return a.URL < b.URL
				}
				return a.Name < b.Name
			},
		},
		{
			Key: sortKeyVersion, Title: "VERSION", Hotkey: 'V', DefaultAsc: true,
			Less: func(a, b *Row) bool {
				if a.Version == "" && b.Version != "" {
					return false
				}
				if a.Version != "" && b.Version == "" {
					return true
				}
				if a.Version != b.Version {
					return semverLess(a.Version, b.Version)
				}
				return a.Name < b.Name
			},
		},
	}
}

// semverLess is a semver-flavoured string comparator. It strips
// a leading "v", splits on ".", and compares each dotted segment
// as int when both sides parse — falling back to a lexical
// remainder compare on the first non-numeric segment. Handles
// the "1.10.0 > 1.2.0" case a default string compare gets wrong;
// release-candidate / build-metadata tails ("0.27.0-rc.1") fall
// through to lexical so the order is at least deterministic.
func semverLess(a, b string) bool {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := min(len(aParts), len(bParts))
	for i := range n {
		an, aerr := strconv.Atoi(aParts[i])
		bn, berr := strconv.Atoi(bParts[i])
		if aerr == nil && berr == nil {
			if an != bn {
				return an < bn
			}
			continue
		}
		return strings.Join(aParts[i:], ".") < strings.Join(bParts[i:], ".")
	}
	return len(aParts) < len(bParts)
}

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
	Styles *theme.Styles
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
	styles *theme.Styles
	drill  func(name string) (app.Page, error)

	// sorter governs the visible row order. Default NAME ASC matches
	// the canonical alphabetical order so first-paint puts the
	// digit-annotated rows in their digit slots; the user can
	// re-sort by URL / VERSION via Shift+letter without affecting
	// the canonical digit binding (annotations key off
	// canonicalRows, not the visible sort).
	sorter *tablesort.Sorter[Row]

	rows []Row
	// window owns the cursor, topRow, and bodyHeight invariants per
	// ADR-0016. Held as a value field rather than embedded because
	// this page does not embed listpage.Base (ADR-0013).
	window cursor.Window

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
	return &Page{
		styles: opts.Styles,
		drill:  opts.DrillFactory,
		scope:  "all",
		sorter: tablesort.New(tenantSortColumns(), sortKeyName),
	}
}

// SetRows replaces the rendered rows. Used by the wiring layer
// instead of a poll.DataMsg path because tenant rows are derived
// from configuration + every (backend, resource) poller — there's
// no single DataMsg shape that fits.
func (p *Page) SetRows(rows []Row) {
	p.rows = rows
	p.window.Clamp(len(rows))
}

func (*Page) Init() tea.Cmd { return nil }

func (*Page) Close() tea.Cmd { return nil }

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

// Bindings implements app.Page. Sort shortcuts come from the
// tablesort helper so the convention reads identically to alerts /
// silences / groups / receivers.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings("tenant")
	out := make([]action.Action, 0, 1+len(sortBindings))
	out = append(out, action.Action{Key: "Enter", Description: "config", View: "tenant"})
	out = append(out, sortBindings...)
	return out
}

func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case app.GoToFirstRowMsg:
		p.window.SetIndex(0, len(p.rows))
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
	// Sort shortcuts run first so Shift+N (sort) doesn't collide
	// with anything else. h/l walk also routes through the helper.
	if p.handleSort(keyMsg) {
		return p, nil
	}
	if _, handled := p.window.MoveCursor(keyMsg.String(), len(p.rows)); handled {
		return p, nil
	}
	if keyMsg.String() == "enter" {
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

// handleSort routes the page's sort hotkeys (h/l walk, Shift+N/U/V)
// through the tablesort helper. Returns true when the key was
// consumed. User-initiated re-sort is k9s-positional: the cursor
// stays at the row index it was at, which, with the rows reordered,
// puts a different backend under the cursor. Tenant data doesn't
// poll, so there's no equivalent of the alerts page's
// focusFingerprint-restore branch — index-stable IS the only
// behaviour the page implements.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	return p.sorter.HandleKey(m.String())
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
	if p.window.Index() >= len(rows) {
		return nil
	}
	name := rows[p.window.Index()].Name
	page, err := p.drill(name)
	if err != nil {
		return footer.ShowFlash(footer.FlashWarn, err.Error())
	}
	return app.PushPage(func() app.Page { return page })
}

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
	p.window.SetViewport(bodyHeight, len(p.rows))
	maxRows := min(bodyHeight, len(p.rows))
	end := min(p.window.TopRow()+maxRows, len(p.rows))
	out := make([]string, 0, end-p.window.TopRow()+1)
	out = append(out, headerLine)
	rows := p.rowsSorted()
	canonical := canonicalDigits(p.canonicalRows())
	for i := p.window.TopRow(); i < end; i++ {
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
		if i == p.window.Index() {
			prefix = "▸ "
		}
		body := digitGlyph + scopeGlyph + " " + p.padTenantColumns(columns, width)
		line := format.PadRight(prefix+body, width)
		switch {
		case i == p.window.Index():
			// k9s parity: cursor bg tracks the row's semantic
			// colour. Tenant rows have no severity / state, so we
			// use Severity.Info (which maps to k9s's StdColor =
			// frame.status.newColor) — the same default k9s uses
			// for resource pages without a row colorer.
			rowColor := p.styles.Severity.Info.GetForeground()
			line = p.styles.Table.CursorOver(rowColor).Render(line)
		case p.scopeIncludes(row.Name):
			line = p.styles.Table.MarkedFg.Render(line)
		}
		out = append(out, line)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

// renderHeader returns the styled column-title row with active-
// column tint + arrow glyph. Mirrors the alerts / silences /
// groups pages' header convention.
func (p *Page) renderHeader(width int) string {
	cols := []struct {
		label string
		key   string
	}{
		{label: "NAME", key: sortKeyName},
		{label: "URL", key: sortKeyURL},
		{label: "VERSION", key: sortKeyVersion},
	}
	// fg-only so the header keeps the terminal default background
	// — painted palette bg in the unstyled body frame creates a
	// coloured stripe.
	headerFg := p.styles.Table.HeaderFg
	activeFg := p.styles.Table.HeaderActiveFg
	parts := make([]string, len(cols))
	widths := tenantColumnWidths(width)
	for i, c := range cols {
		label := c.label
		if arrow := p.sorter.ArrowFor(c.key); arrow != "" {
			label = label + " " + arrow
		}
		padded := format.PadRight(label, widths[i])
		if p.sorter.IsActive(c.key) {
			parts[i] = activeFg.Render(padded)
		} else {
			parts[i] = headerFg.Render(padded)
		}
	}
	// Match the per-row prefix region so columns align with their
	// headers; tenantRowPrefixCols owns the canonical width.
	return strings.Repeat(" ", tenantRowPrefixCols) + strings.Join(parts, "")
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
// tenantRowPrefixCols accounts for the per-row decoration before
// the data columns: "▸ " (cursor, 2) + "[N] " (canonical digit, 4)
// + "●" (scope glyph, 1) + " " (separator, 1). Both renderHeader
// and tenantColumnWidths reference this so the two stay aligned —
// drift between them shoves headers and rows out of sync.
const (
	tenantColName       = 16
	tenantColVersion    = 14
	tenantRowPrefixCols = 8
)

// padTenantColumns lays out a row across NAME / URL (flex) /
// VERSION columns at fixed widths with URL absorbing the
// remaining width.
func (p *Page) padTenantColumns(parts []string, width int) string {
	if len(parts) == 0 {
		return ""
	}
	cols := tenantColumnWidths(width)
	var b strings.Builder
	b.Grow(width + 64)
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(format.PadRight(v, cols[i]))
	}
	return b.String()
}

// tenantColumnWidths returns the per-column widths (NAME, URL flex,
// VERSION) the header and per-row layouts both reference. Extracted
// so renderHeader can pad each column to its individual width before
// applying per-cell styling — padTenantColumns concatenates raw
// padded strings, but per-cell styling needs widths separately.
func tenantColumnWidths(width int) []int {
	used := tenantColName + tenantColVersion + tenantRowPrefixCols
	flex := max(width-used, 16)
	return []int{tenantColName, flex, tenantColVersion}
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

// rowsSorted returns the rows ordered by the active sort column +
// direction. Defaults to NAME ASC (matching the canonical /
// quick-switch order) so a fresh page paints with digit
// annotations aligned to row positions; user re-sort by URL /
// VERSION reorders the visible rows independently while the
// canonical digit annotations stay anchored to alphabetical-by-
// name order via canonicalRows.
//
// VERSION sort post-processes empty Version rows to the bottom of
// the slice in BOTH directions: empty is "unknown", not "lowest"
// or "highest", and Sorter.Apply's argument-flip alone can't
// express "always last" without direction-aware Less. The hard
// pin here is what makes the operator-priority "concrete versions
// at the top" UX hold under DESC as well as ASC.
func (p *Page) rowsSorted() []Row {
	out := make([]Row, len(p.rows))
	copy(out, p.rows)
	p.sorter.Apply(out)
	if p.sorter.ActiveKey() == sortKeyVersion {
		out = pinEmptyVersionsToEnd(out)
	}
	return out
}

// pinEmptyVersionsToEnd reorders rows so any Row with an empty
// Version sits after every Row with a non-empty Version, while
// preserving the relative order Sorter.Apply produced within each
// group. Stable in the sense that callers can compose it with the
// helper's stable Apply without losing tie-break determinism.
func pinEmptyVersionsToEnd(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	tail := make([]Row, 0)
	for _, r := range rows {
		if r.Version == "" {
			tail = append(tail, r)
			continue
		}
		out = append(out, r)
	}
	return append(out, tail...)
}

// canonicalRows returns the rows sorted alphabetically by Name —
// the canonical order the global numeric quick-switch <1>-<9>
// binds to, regardless of the user's visible sort. Used to build
// the digit-annotation map so the digit a backend wears stays
// stable across visible re-sorts.
func (p *Page) canonicalRows() []Row {
	out := make([]Row, len(p.rows))
	copy(out, p.rows)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
