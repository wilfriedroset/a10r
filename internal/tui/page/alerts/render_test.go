// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// rowContaining returns the first rendered line containing sub.
func rowContaining(t *testing.T, out, sub string) string {
	t.Helper()
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	t.Fatalf("no rendered line contains %q\n%s", sub, out)
	return ""
}

// TestRender_NoColumnFusionAt80 asserts that the widest ALERTNAME /
// STATE row keeps a visible gap before COUNT and before AGE — the
// fusion defect (ALERTNAMECOUNT, suppressed9m ago) must be gone, and
// the fixed columns must still render their content.
func TestRender_NoColumnFusionAt80(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	alerts := make([]backend.Alert, 0, 12)
	for i := range 9 {
		alerts = append(alerts, mkAlert("ALERTNAME", "critical", backend.AlertStateActive,
			"a"+string(rune('0'+i)), time.Duration(i)*time.Minute, map[string]string{"instance": "x" + string(rune('0'+i))}))
	}
	for i := range 3 {
		alerts = append(alerts, mkAlert("ALERTNAME", "critical", backend.AlertStateSuppressed,
			"s"+string(rune('0'+i)), 9*time.Minute, map[string]string{"instance": "y" + string(rune('0'+i))}))
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	out := testutil.StripStyle(p.View(80, 24))
	// data row (count is 12: 9 active + 3 suppressed)
	row := rowContaining(t, out, "12")

	// COUNT (12) must be present and separated from ALERTNAME.
	require.Contains(t, row, "12")
	require.NotContains(t, row, "ALERTNAME12", "ALERTNAME must not fuse with COUNT")
	// AGE must be present and separated from the STATE cell.
	require.Contains(t, row, "9m ago")
	require.NotRegexp(t, `[a-z…]9m ago`, row, "STATE must not fuse with AGE")

	// SEVERITY and COUNT keep their content (not shrunk to nothing).
	require.Contains(t, row, "critical")
}

// TestRender_StateBreakdownEllipsizesUnderCap asserts a 3-bucket full
// breakdown renders ellipsized at a narrow STATE width rather than
// widening past the cap or starving ALERTNAME. The compact form stays
// dense and unaffected.
func TestRender_StateBreakdownEllipsizesUnderCap(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	alerts := make([]backend.Alert, 0, 13)
	for i := range 9 {
		alerts = append(alerts, mkAlert("ALERTNAME", "critical", backend.AlertStateActive,
			"a"+string(rune('0'+i)), time.Duration(i)*time.Minute, map[string]string{"instance": "x" + string(rune('0'+i))}))
	}
	for i := range 3 {
		alerts = append(alerts, mkAlert("ALERTNAME", "critical", backend.AlertStateSuppressed,
			"s"+string(rune('0'+i)), 9*time.Minute, map[string]string{"instance": "y" + string(rune('0'+i))}))
	}
	alerts = append(alerts, mkAlert("ALERTNAME", "critical", backend.AlertStateUnprocessed,
		"u0", 9*time.Minute, map[string]string{"instance": "z"}))
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	// Full: the 3-bucket breakdown exceeds the STATE cap and must be
	// ellipsized — the ellipsis glyph appears, the full tail does not.
	out := testutil.StripStyle(p.View(80, 24))
	row := rowContaining(t, out, "active")
	require.Contains(t, row, "…", "the over-cap STATE breakdown must ellipsize")
	require.NotContains(t, row, "1 unprocessed", "the full tail must not survive past the cap")

	// STATE's measured content must not drive the column past the cap:
	// ALERTNAME (the flex column) must keep room for its own glyphs.
	widths := p.columnWidths(80)
	stateIdx := len(widths) - 2
	require.LessOrEqual(t, widths[stateIdx], stateContentCap,
		"STATE width must not exceed the cap")

	// Compact: 3 buckets fit, no ellipsis needed.
	p.stateFormat = stateformat.Compact
	out = testutil.StripStyle(p.View(80, 24))
	row = rowContaining(t, out, "9ac")
	require.Contains(t, row, "9ac 3su 1un", "compact breakdown stays dense and full")
}
