// SPDX-License-Identifier: Apache-2.0

package format_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

func TestDistribute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		cols      []format.Column
		total     int
		separator int
		want      []int
	}{
		{
			name: "fixed columns get max(min, content); flex column eats the remainder",
			cols: []format.Column{
				{Min: 4, Content: 4, Weight: 0},    // SEVERITY
				{Min: 10, Content: 100, Weight: 1}, // ALERTNAME (flex)
				{Min: 8, Content: 8, Weight: 0},    // STATE
				{Min: 6, Content: 6, Weight: 0},    // AGE
			},
			total:     80,
			separator: 0,
			want:      []int{4, 62, 8, 6}, // 4+62+8+6 == 80
		},
		{
			name: "two flex columns split the remainder by weight",
			cols: []format.Column{
				{Min: 4, Content: 4, Weight: 0},
				{Min: 10, Content: 100, Weight: 3}, // 75% share
				{Min: 10, Content: 100, Weight: 1}, // 25% share
			},
			total:     84,
			separator: 0,
			// reserved = 4 + 10 + 10 = 24 ⇒ remainder 60. 75% / 25%
			// split adds 45 / 15 to the already-reserved 10 / 10.
			want: []int{4, 55, 25},
		},
		{
			name: "flex column capped at Content gives slack to its uncapped peer",
			cols: []format.Column{
				{Min: 4, Content: 4, Weight: 0},
				{Min: 0, Content: 6, Weight: 1}, // would-be 25; capped at 6
				{Min: 0, Content: 200, Weight: 1},
			},
			total:     100,
			separator: 0,
			// reserved = 4; remainder = 96. Equal weights => 48 each.
			// First flex caps at 6, releasing 42 to the second flex.
			want: []int{4, 6, 90},
		},
		{
			name: "weight=0 column never grows past Content even with budget left",
			cols: []format.Column{
				{Min: 4, Content: 4, Weight: 0},   // tight
				{Min: 0, Content: 12, Weight: 0},  // exact content
				{Min: 0, Content: 200, Weight: 1}, // unbounded
			},
			total:     80,
			separator: 0,
			want:      []int{4, 12, 64},
		},
		{
			name: "all-zero weights with content room to spare leaves slack on the floor",
			cols: []format.Column{
				{Min: 0, Content: 5, Weight: 0},
				{Min: 0, Content: 5, Weight: 0},
				{Min: 0, Content: 5, Weight: 0},
			},
			total:     80,
			separator: 0,
			// Reserved = 15; no flex column claims the remaining 65.
			// Slack stays unallocated — the renderer pads with spaces.
			want: []int{5, 5, 5},
		},
		{
			name: "extreme narrow terminal proportionally shrinks fixed columns",
			cols: []format.Column{
				{Min: 4, Content: 4, Weight: 0},
				{Min: 8, Content: 100, Weight: 1},
				{Min: 8, Content: 8, Weight: 0},
			},
			total:     10,
			separator: 0,
			// Reservation 4+8+8 = 20 > budget 10 ⇒ shrink pro-rata to
			// hit total. 4*10/20=2, 8*10/20=4, 8*10/20=4 ⇒ 2+4+4=10.
			want: []int{2, 4, 4},
		},
		{
			name: "single ultra-long label keeps every other column at minimum",
			cols: []format.Column{
				{Min: 4, Content: 4, Weight: 0},
				{Min: 8, Content: 4096, Weight: 1}, // unbounded label
				{Min: 8, Content: 8, Weight: 0},
				{Min: 6, Content: 6, Weight: 0},
			},
			total:     60,
			separator: 0,
			// Reservation 4+8+8+6 = 26 ⇒ remainder 34 ⇒ all goes to
			// the only flex column atop its 8-cell minimum.
			want: []int{4, 42, 8, 6},
		},
		{
			name: "separator overhead reduces the distributable budget",
			cols: []format.Column{
				{Min: 4, Content: 4, Weight: 0},
				{Min: 0, Content: 200, Weight: 1},
				{Min: 8, Content: 8, Weight: 0},
			},
			total:     30,
			separator: 1,
			// 3 columns => 2 separators => budget 28; reserved 12 ⇒
			// flex gets 16.
			want: []int{4, 16, 8},
		},
		{
			name: "zero total returns all-zero widths",
			cols: []format.Column{
				{Min: 4, Content: 10, Weight: 0},
				{Min: 0, Content: 100, Weight: 1},
			},
			total:     0,
			separator: 0,
			want:      []int{0, 0},
		},
		{
			name:      "empty cols returns nil",
			cols:      nil,
			total:     80,
			separator: 0,
			want:      nil,
		},
		{
			name: "negative total clamped to zero",
			cols: []format.Column{
				{Min: 4, Content: 10, Weight: 0},
				{Min: 0, Content: 100, Weight: 1},
			},
			total:     -7,
			separator: 0,
			want:      []int{0, 0},
		},
		{
			name: "small residual goes to the highest-weight uncapped flex column",
			cols: []format.Column{
				{Min: 0, Content: 100, Weight: 1},
				{Min: 0, Content: 100, Weight: 2},
				{Min: 0, Content: 100, Weight: 3},
			},
			total:     7, // < weightSum=6 in the residual loop
			separator: 0,
			// First pass: 7*1/6=1, 7*2/6=2, 7*3/6=3 ⇒ totals 6, leaving
			// remainder 1 for the residual handler. Highest weight (3rd
			// col) wins the cell.
			want: []int{1, 2, 4},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := format.Distribute(tc.cols, tc.total, tc.separator)
			require.Equal(t, tc.want, got)

			if got != nil {
				sum := 0
				for _, w := range got {
					require.GreaterOrEqual(t, w, 0,
						"column widths must never be negative")
					sum += w
				}
				if tc.total > 0 {
					sepBudget := 0
					if len(got) > 1 && tc.separator > 0 {
						sepBudget = (len(got) - 1) * tc.separator
					}
					require.LessOrEqual(t, sum+sepBudget, tc.total,
						"sum(widths) + separators must not exceed total")
				}
			}
		})
	}
}

func TestEllipsize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		s    string
		w    int
		want string
	}{
		{name: "fits unchanged", s: "abcd", w: 6, want: "abcd"},
		{name: "exact width unchanged", s: "abcd", w: 4, want: "abcd"},
		{name: "truncates with ellipsis suffix", s: "abcdef", w: 4, want: "abc" + format.EllipsizeSuffix},
		{name: "single-cell width returns suffix alone", s: "abcdef", w: 1, want: format.EllipsizeSuffix},
		{name: "zero width returns empty", s: "abcdef", w: 0, want: ""},
		{name: "negative width returns empty", s: "abcdef", w: -2, want: ""},
		{name: "empty input returns empty even with width", s: "", w: 5, want: ""},
		// CJK: each glyph is 2 cells. "你好世" is 6 cells; w=5 yields
		// Truncate(s, 4) = "你好" (4 cells) + suffix = 5 cells —
		// always whole glyphs, never wider than w.
		{name: "CJK truncates whole glyphs and adds ellipsis", s: "你好世", w: 5, want: "你好" + format.EllipsizeSuffix}, //nolint:gosmopolitan // intentional Han literal
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := format.Ellipsize(tc.s, tc.w)
			require.Equal(t, tc.want, got)
			if tc.w > 0 {
				require.LessOrEqual(t, lipgloss.Width(got), tc.w,
					"Ellipsize output must never exceed w cells")
			}
		})
	}
}

// TestDistribute_RealisticAlertsTable replays the alerts page's
// actual column shape (TENANT optional + SEVERITY + ALERTNAME flex
// + STATE + AGE) at three terminal widths to lock in the
// regression contract: the flex column expands on a wide terminal
// and the row sum stays inside the budget on a narrow one.
func TestDistribute_RealisticAlertsTable(t *testing.T) {
	t.Parallel()

	cols := []format.Column{
		{Min: 12, Content: 12, Weight: 0},  // SEVERITY
		{Min: 10, Content: 256, Weight: 1}, // ALERTNAME (flex)
		{Min: 14, Content: 14, Weight: 0},  // STATE
		{Min: 12, Content: 12, Weight: 0},  // AGE
	}
	t.Run("wide terminal", func(t *testing.T) {
		t.Parallel()
		got := format.Distribute(cols, 200, 0)
		require.Greater(t, got[1], 100,
			"the flex column expands when the terminal has cells to spare")
	})
	t.Run("narrow terminal", func(t *testing.T) {
		t.Parallel()
		got := format.Distribute(cols, 60, 0)
		sum := 0
		for _, w := range got {
			sum += w
		}
		require.LessOrEqual(t, sum, 60, "row never overflows the terminal")
		require.Equal(t, 12, got[0])
		require.Equal(t, 14, got[2])
		require.Equal(t, 12, got[3])
	})
	t.Run("bizarrely narrow terminal", func(t *testing.T) {
		t.Parallel()
		got := format.Distribute(cols, 20, 0)
		sum := 0
		for _, w := range got {
			sum += w
		}
		require.LessOrEqual(t, sum, 20)
	})
	t.Run("ellipsize on flex column shortfall", func(t *testing.T) {
		t.Parallel()
		// Realistic per-cell render: flex column gets w cells, label
		// is wider, format.Ellipsize must add the … suffix.
		got := format.Distribute(cols, 60, 0)
		flexW := got[1]
		long := strings.Repeat("a", 64)
		out := format.Ellipsize(long, flexW)
		require.True(t, strings.HasSuffix(out, format.EllipsizeSuffix),
			"long label must be ellipsized with the … suffix when the column is too narrow")
		require.LessOrEqual(t, lipgloss.Width(out), flexW,
			"ellipsized output never exceeds the assigned column width")
	})
}
