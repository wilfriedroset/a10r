// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	p.bodyHeight = height - 1 // header takes one line; the rest is table-row budget
	if len(p.view) == 0 {
		// Render bg-less so the empty state matches the regular
		// table view's framing — both use the terminal default
		// background. styles.Body.Default would paint the body
		// palette behind the empty pane, which renders as a
		// coloured patch the populated view doesn't have, breaking
		// the visual parity between "loading" and "loaded" frames.
		return lipgloss.NewStyle().Width(width).Height(height).Render(p.emptyState())
	}
	headerLine := p.renderHeader(width)
	rows := p.renderRows(width, height-1)
	body := headerLine + "\n" + rows
	return lipgloss.NewStyle().Width(width).Render(body)
}

// emptyState picks the right body for an empty list. The cold-
// start / refresh-in-flight loading hint now lives in the title
// (Title swaps to "<spinner> loading silences…" while !polled
// or refreshing), so the body stays empty in that window — no
// duplicate spinner. After the first DataMsg lands and there's
// genuinely nothing to show, the body explains why: "no silences
// (yet)" for an empty backend, "no silences in view" when a
// filter is the cause.
func (p *Page) emptyState() string {
	if !p.polled() || p.refreshing {
		return ""
	}
	if p.totalSilences() == 0 {
		return "no silences (yet)"
	}
	return "no silences in view"
}

// renderHeader returns the styled, uppercased column-title row
// with a sort marker on the active column. theme.Table.Header
// applies the k9s-style yellow header colour. When the active
// scope spans more than one tenant, a leading TENANT column is
// inserted so the user knows which backend each row came from.
// Sort markers ride only on the four sortable columns (BY,
// STARTS, ENDS, STATE) — UUID and COMMENT are display-only.
//
// The leading whitespace mirrors the per-row prefix so column
// titles line up with their data: always two cols for the cursor
// slot ("▸ " / "  "), plus another two for the mark glyph
// ("✓ " / "  ") when any row is marked.
func (p *Page) renderHeader(width int) string {
	type col struct {
		label   string
		sortKey string // "" when the column is display-only (UUID, COMMENT, TENANT)
	}
	cols := make([]col, 0, 7)
	if p.showTenantColumn() {
		cols = append(cols, col{label: "TENANT"})
	}
	cols = append(cols,
		col{label: "UUID"},
		col{label: "BY", sortKey: sortKeyCreatedBy},
		col{label: "COMMENT"},
		col{label: "STARTS", sortKey: sortKeyStartsAt},
		col{label: "ENDS", sortKey: sortKeyEndsAt},
		col{label: "STATE", sortKey: sortKeyState},
	)
	// fg-only so the header keeps the terminal default background
	// — painted palette bg in the unstyled body frame creates a
	// coloured stripe.
	headerFg := theme.FgOnly(p.styles.Table.Header.GetForeground())
	activeFg := theme.FgOnly(p.styles.Table.HeaderActive.GetForeground())
	parts := make([]string, len(cols))
	for i, c := range cols {
		label := c.label
		if c.sortKey != "" {
			if arrow := p.sorter.ArrowFor(c.sortKey); arrow != "" {
				label = label + " " + arrow
			}
		}
		// Active sort column gets HeaderActive; everything else
		// (sortable-but-inactive plus display-only) gets the regular
		// Header foreground — both cues (column tint + arrow) point
		// at the same axis.
		if c.sortKey != "" && p.sorter.IsActive(c.sortKey) {
			parts[i] = activeFg.Render(label)
		} else {
			parts[i] = headerFg.Render(label)
		}
	}
	leading := "  "
	if p.hasMarks() {
		leading = "    "
	}
	return leading + p.padColumns(parts, width)
}

// hasMarks reports whether any silence ID is currently marked.
// Inlined-style helper so the renderer can branch without
// poking at p.marks length in two places.
func (p *Page) hasMarks() bool { return len(p.marks) > 0 }

func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	p.topRow = cursor.ReconcileScroll(p.cursor, p.topRow, maxRows, len(p.view))
	end := min(p.topRow+maxRows, len(p.view))
	showMark := p.hasMarks()
	var b strings.Builder
	for i := p.topRow; i < end; i++ {
		e := p.view[i]
		row := make([]string, 0, 7)
		if p.showTenantColumn() {
			row = append(row, e.tenant)
		}
		row = append(row,
			clipSilenceID(e.s.ID),
			e.s.CreatedBy,
			singleLine(e.s.Comment),
			p.formatTime(e.s.StartsAt),
			p.formatTime(e.s.EndsAt),
			string(e.s.State),
		)
		prefix := "  "
		if i == p.cursor {
			prefix = "▸ "
		}
		_, marked := p.marks[e.s.ID]
		mark := ""
		if showMark {
			if marked {
				mark = "✓ "
			} else {
				mark = "  "
			}
		}
		// Pad to the full width before styling so the Cursor row's
		// background extends across the whole line k9s-style.
		// Precedence: cursor > marked > expired-dim. Cursor wraps
		// the whole row in fg+bg (the salient "you are here"
		// signal); Marked and the expired-dim treatment both change
		// the foreground only so the row keeps the body's default
		// background — k9s "tinted text" rather than two competing
		// highlighted stripes stacked on top of each other. Dimming
		// fires when the silence is expired (state == expired) and
		// is neither cursor nor marked — same treatment the alerts
		// page applies to suppressed alerts. Marked beats the dim:
		// marked is an explicit user action, expiry is ambient
		// state.
		line := format.PadRight(prefix+mark+p.padColumns(row, width), width)
		switch {
		case i == p.cursor:
			// k9s parity: cursor bg tracks the silence-state
			// colour (active/pending/expired) rather than the
			// static cursorBgColor — see select_table.go:128 in
			// k9s for the equivalent runtime override.
			rowColor := silenceStateColor(e.s.State, p.styles)
			line = p.styles.Table.CursorOver(rowColor).Render(line)
		case marked:
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Marked.GetForeground()).
				Render(line)
		case e.s.State == backend.SilenceStateExpired:
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Dimmed.GetForeground()).
				Render(line)
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// padColumns lays out a row across fixed-width columns. UUID,
// BY, STARTS, ENDS, and STATE are fixed; COMMENT takes the
// remaining flex so a long Silence.Comment gets the full
// breathing room instead of competing with another text column.
// STARTS / ENDS widen in absolute time mode so the ISO local
// timestamp fits without truncation per Q7.4.
func (p *Page) padColumns(parts []string, width int) string {
	const (
		tenantW = 16
		uuidW   = 10
		byW     = 16 // fits typical 14-char human user names with a 2-col gap
		stateW  = 12
		minDesc = 12
	)
	startsW, endsW := 14, 14
	if p.timeFormat == app.TimeFormatAbsolute {
		startsW, endsW = 20, 20
	}
	fixed := uuidW + byW + startsW + endsW + stateW
	cols := make([]int, 0, 7)
	if p.showTenantColumn() {
		cols = append(cols, tenantW)
		fixed += tenantW
	}
	descW := max(width-fixed, minDesc)
	cols = append(cols, uuidW, byW, descW, startsW, endsW, stateW)
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(padCell(v, cols[i]))
	}
	return b.String()
}

// padCell pads s to exactly w display cols and guarantees at
// least one trailing whitespace col so adjacent cells never
// visually merge. Content that meets or exceeds the budget is
// clipped to w-2 with an ellipsis + space appended so the user
// sees both that the cell was truncated and where it ends. The
// gap rule is what fixes `juliette.oraincreated…` and
// `…sys_id=02e61a8619h ago` overlap reports.
func padCell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := lipgloss.Width(s)
	if sw <= w-1 {
		return s + strings.Repeat(" ", w-sw)
	}
	if w == 1 {
		return " "
	}
	return format.Truncate(s, w-2) + "… "
}

// formatTime renders ts according to the page's active time
// format. Mirrors the alerts / alert-detail formatters.
func (p *Page) formatTime(ts time.Time) string {
	if p.timeFormat == app.TimeFormatAbsolute {
		return header.FormatAbsolute(ts)
	}
	return header.FormatAge(p.now(), ts)
}

// clipSilenceID returns the leading 8 chars of id so the UUID
// column stays compact. Full IDs remain searchable through the
// filter prompt — silenceMatches scans the unclipped value, so
// an operator can paste any prefix they remember and still find
// the row.
func clipSilenceID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// singleLine flattens any newline / carriage-return / tab inside
// s into a regular space so a multi-line Silence.Comment doesn't
// break the table row alignment. Operators routinely paste URLs
// or runbook excerpts on their own line; without this the COMMENT
// cell's embedded \n shoves STARTS / ENDS / STATE onto the next
// physical line, mid-URL.
func singleLine(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
}

// silenceStateColor returns the foreground color associated with a
// silence's state. Used to drive the cursor row's bg per the k9s
// pattern where the selected row's bg tracks the row's semantic
// colour rather than a static cursorBgColor.
func silenceStateColor(s backend.SilenceState, styles theme.Styles) color.Color {
	switch s {
	case backend.SilenceStateActive:
		return styles.SilenceState.Active.GetForeground()
	case backend.SilenceStatePending:
		return styles.SilenceState.Pending.GetForeground()
	case backend.SilenceStateExpired:
		return styles.SilenceState.Expired.GetForeground()
	}
	return styles.SilenceState.Active.GetForeground()
}
