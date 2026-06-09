// SPDX-License-Identifier: Apache-2.0

// Package tenant renders a read-only table of configured backends
// (NAME / URL / VERSION); Enter drills into the per-tenant config
// inspector (tenantconfig package).
package tenant

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Sort column keys handed to the tablesort helper. Default NAME ASC
// matches the canonical order so digit annotations align with row
// positions on first paint.
const (
	sortKeyName    = "name"
	sortKeyURL     = "url"
	sortKeyVersion = "version"
)

const scopeAll = "all"

// tenantSortColumns returns the page's sortable columns. The version
// comparator is semver-aware ("0.27.0" sorts after "0.9.0"), and
// empty versions sort LAST ("unknown", not "lowest") so concrete
// versions surface at the top regardless of direction.
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

// semverLess compares dotted segments numerically when both parse,
// handling the "1.10.0 > 1.2.0" case a string compare gets wrong;
// non-numeric tails ("0.27.0-rc.1") fall through to a lexical
// remainder compare so the order stays deterministic.
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

// Row is one tenant's renderable state. Conn / Alerts / Silence are
// deliberately absent: their zero values (header.ConnState zero is
// ConnConnected) would read as "connected, zero alerts" by accident.
type Row struct {
	Name    string
	URL     string
	Version string
}

// Options bundles the constructor inputs.
type Options struct {
	Styles *theme.Styles
	// DrillFactory builds the destination page on Enter; a non-nil
	// error flashes instead of pushing (misconfigured backend).
	// Required: a nil factory makes Enter a silent, undebuggable no-op.
	DrillFactory func(name string) (app.Page, error)
}

// Page is the tenant table view.
type Page struct {
	styles *theme.Styles
	drill  func(name string) (app.Page, error)

	// sorter governs the visible row order; digit annotations key off
	// canonicalRows, not the visible sort, so re-sorting by URL /
	// VERSION leaves the canonical digit binding intact.
	sorter *tablesort.Sorter[Row]

	rows []Row
	// window owns the cursor / topRow / bodyHeight invariants per
	// ADR-0016. A value field rather than embedded because this page
	// does not embed listpage.Base (ADR-0013).
	window cursor.Window

	// scope mirrors the active tenant scope from app.ScopeChangedMsg.
	// The page does NOT mutate it (the numeric quick-switch owns
	// that); it only reflects the App's announcement.
	scope string
}

func New(opts Options) *Page {
	return &Page{
		styles: opts.Styles,
		drill:  opts.DrillFactory,
		scope:  scopeAll,
		sorter: tablesort.New(tenantSortColumns(), sortKeyName),
	}
}

// SetRows replaces the rendered rows. Used instead of a poll.DataMsg
// path because tenant rows derive from config plus every (backend,
// resource) poller, with no single DataMsg shape that fits.
func (p *Page) SetRows(rows []Row) {
	p.rows = rows
	p.window.Clamp(len(rows))
}

func (*Page) Init() tea.Cmd { return nil }

func (*Page) Close() tea.Cmd { return nil }

func (*Page) Crumb() string { return "tenant" }

// Title mirrors the other list pages: `tenants(<scope>)[<count>]`.
func (p *Page) Title() string {
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	return fmt.Sprintf("tenants(%s)[%d]", scope, len(p.rows))
}

func (*Page) HeaderContent() string { return "" }

func (*Page) Footer() string { return "" }

// Bindings sources sort shortcuts from the tablesort helper so the
// convention matches alerts / silences / receivers.
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
		// Emitted by the App's LayerGlobal numeric quick-switch;
		// mirrored here so the table shows the fanned-out row.
		p.scope = m.Scope
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	// Sort shortcuts run first so Shift+N doesn't collide downstream.
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
	// Numeric quick-switch (0, 1-9) is owned by the App's LayerGlobal
	// binding (app.registerTenantBindings), consumed before
	// forwardToTop runs, so the page does NOT bind the digits locally.
	return p, nil
}

// handleSort routes sort hotkeys through the tablesort helper,
// returning true when consumed. Re-sort is k9s-positional (cursor
// stays at its index); tenant data doesn't poll, so there's no
// fingerprint-restore branch like the alerts page has.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	return p.sorter.HandleKey(m.String())
}

// drillToConfig pushes the factory's page, or flashes its error.
// Reads rowsSorted (not p.rows) so the drill matches the row under
// the cursor; nil factory or empty rows are silent no-ops.
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
		// bg-less so the empty pane keeps the terminal default the
		// populated frame uses.
		return listpage.Pane(width, height, "no backends configured")
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
		// ● marks rows in the active global scope.
		scopeGlyph := " "
		if p.scopeIncludes(row.Name) {
			scopeGlyph = "●"
		}
		// Digit annotation for the first 9 backends, matching the
		// quick-switch <1>-<9> binding. Always 4 cols ("[N] " or
		// "    ") so alignment holds whether or not a digit shows.
		digitGlyph := p.styles.Table.DimmedFg.Render(canonical[row.Name])
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
			// k9s parity: tenant rows have no severity, so the cursor
			// bg uses Severity.Info (k9s's StdColor), the default for
			// resource pages without a row colorer.
			rowColor := p.styles.Severity.Info.GetForeground()
			line = p.styles.Table.CursorOver(rowColor).Render(line)
		case p.scopeIncludes(row.Name):
			line = p.styles.Table.MarkedFg.Render(line)
		}
		out = append(out, line)
	}
	return listpage.Wrap(width, strings.Join(out, "\n"))
}

// renderHeader returns the styled column-title row with active-column
// tint and arrow glyph, mirroring the other list pages.
func (p *Page) renderHeader(width int) string {
	cols := []struct {
		label string
		key   string
	}{
		{label: "NAME", key: sortKeyName},
		{label: "URL", key: sortKeyURL},
		{label: "VERSION", key: sortKeyVersion},
	}
	// fg-only: painting a palette bg in the unstyled body frame would
	// leave a coloured stripe.
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
	// Match the per-row prefix region (tenantRowPrefixCols) so columns
	// align with their headers.
	return strings.Repeat(" ", tenantRowPrefixCols) + strings.Join(parts, "")
}

// canonicalDigits maps name → "[N] " for the first 9 backends (by
// name); the rest map to "    " so the per-row layout stays constant
// width.
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

// Column widths; URL is the flex column. tenantRowPrefixCols is the
// per-row decoration width: "▸ "(2) + "[N] "(4) + "●"(1) + " "(1).
// Both renderHeader and tenantColumnWidths reference it; drift would
// shove headers and rows out of sync.
const (
	tenantColName       = 16
	tenantColVersion    = 14
	tenantRowPrefixCols = 8
)

// padTenantColumns lays out a row across NAME / URL (flex) / VERSION.
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
// VERSION) shared by the header and per-row layouts. Extracted so
// renderHeader can pad each column before applying per-cell styling.
func tenantColumnWidths(width int) []int {
	used := tenantColName + tenantColVersion + tenantRowPrefixCols
	flex := max(width-used, 16)
	return []int{tenantColName, flex, tenantColVersion}
}

// scopeIncludes reports whether the named tenant is in the active
// global scope. "all" / empty includes everyone; otherwise the name
// is matched against the comma-joined scope list.
func (p *Page) scopeIncludes(name string) bool {
	scope := strings.TrimSpace(p.scope)
	if scope == "" || scope == scopeAll {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == name {
			return true
		}
	}
	return false
}

// rowsSorted returns rows in the active sort order. VERSION sort
// pins empty-Version rows to the end in BOTH directions: empty is
// "unknown", not "lowest"/"highest", which Sorter.Apply's
// argument-flip alone can't express without direction-aware Less.
func (p *Page) rowsSorted() []Row {
	out := make([]Row, len(p.rows))
	copy(out, p.rows)
	p.sorter.Apply(out)
	if p.sorter.ActiveKey() == sortKeyVersion {
		out = pinEmptyVersionsToEnd(out)
	}
	return out
}

// pinEmptyVersionsToEnd moves empty-Version rows after non-empty
// ones, preserving the relative order within each group so it
// composes with Sorter.Apply's stable tie-break.
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

// canonicalRows returns rows sorted by Name — the order the numeric
// quick-switch <1>-<9> binds to regardless of the visible sort, so a
// backend's digit annotation stays stable across re-sorts.
func (p *Page) canonicalRows() []Row {
	out := make([]Row, len(p.rows))
	copy(out, p.rows)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
