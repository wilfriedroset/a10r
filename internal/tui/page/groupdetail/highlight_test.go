// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// highlightPage seeds the cursor row (web-0), a plain firing row
// (web-1) and a dimmed suppressed row (web-2). Every instance value
// contains "web", so every row survives a "web" filter.
func highlightPage(t *testing.T) *Page {
	t.Helper()
	return newPage(t,
		instance("fp-0", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst0}),
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		instance("fp-2", "warning", backend.AlertStateSuppressed, map[string]string{sortKeyInstance: webInst2}),
	)
}

// rawRowContaining returns the first rendered line whose visible text
// contains sub, with its styling kept.
func rawRowContaining(t *testing.T, out, sub string) string {
	t.Helper()
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(testutil.StripStyle(l), sub) {
			return l
		}
	}
	t.Fatalf("no rendered line shows %q\n%s", sub, testutil.StripStyle(out))
	return ""
}

func filteredView(t *testing.T, p *Page, filter string) string {
	t.Helper()
	require.True(t, p.SetFilter(filter))
	p.recompute()
	return p.View(120, 20)
}

func TestHighlight_KeepsColumnsPut(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	plain := testutil.StripStyle(filteredView(t, p, ""))
	for _, filter := range []string{"web", "~wb", `\web`, "web-.*", "warn"} {
		require.Equal(t, plain, testutil.StripStyle(filteredView(t, p, filter)),
			"filter %q moved the text", filter)
	}
}

func TestHighlight_PaintsThePlainRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filter string
		match  string
	}{
		{filter: "web", match: "web"},
		{filter: "warn", match: "warn"},
	}
	for _, tc := range tests {
		t.Run(tc.filter, func(t *testing.T) {
			t.Parallel()
			p := highlightPage(t)
			row := rawRowContaining(t, filteredView(t, p, tc.filter), webInst1)
			require.Contains(t, row, p.styles.Table.MatchFg.Render(tc.match))
		})
	}
}

func TestHighlight_UnderlinesTheStyledRows(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	out := filteredView(t, p, "web")
	for _, inst := range []string{webInst0, webInst2} {
		row := rawRowContaining(t, out, inst)
		require.Contains(t, row, format.Emphasis("web"), "row %s", inst)
		require.NotContains(t, row, p.styles.Table.MatchFg.Render("web"), "row %s", inst)
	}
}
