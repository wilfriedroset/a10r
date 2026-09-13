// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// highlightPage builds two active silences that both survive the
// filter "bob", so the filtered and the unfiltered render lay out the
// same columns.
func highlightPage(t *testing.T) *Page {
	t.Helper()
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Silence{
		pagetest.Silence(pagetest.SilenceOptions{ID: "sil-1", CreatedBy: "bob", State: backend.SilenceStateActive, EndsIn: time.Hour}),
		pagetest.Silence(pagetest.SilenceOptions{ID: "sil-2", CreatedBy: "bobby", State: backend.SilenceStateActive, EndsIn: 2 * time.Hour}),
	}})
	return p
}

// TestHighlight_KeepsColumnsPut is the layout contract: the painted
// render must carry the same text, in the same columns, as the
// unfiltered render of the same rows.
func TestHighlight_KeepsColumnsPut(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	plain := testutil.StripStyle(p.View(120, 24))

	for _, filter := range []string{"bob", "~bb", `\bob`, ".*bob"} {
		p.Filter = filter
		p.recompute()
		require.Equal(t, plain, testutil.StripStyle(p.View(120, 24)), "filter %q moved the text", filter)
	}
}

// TestHighlight_PaintsRows asserts both row kinds: the cursor row
// keeps its own style across the match, the plain row takes the
// match colour.
func TestHighlight_PaintsRows(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	p.Filter = "bob"
	p.recompute()
	p.SetIndex(0, 2)

	out := p.View(120, 24)
	var cursor, plain string
	for l := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.Contains(testutil.StripStyle(l), "bobby"):
			plain = l
		case strings.Contains(testutil.StripStyle(l), "bob"):
			cursor = l
		}
	}
	require.NotEmpty(t, cursor)
	require.NotEmpty(t, plain)
	require.Contains(t, cursor, format.Emphasis("bob"))
	require.Contains(t, plain, p.styles.Table.MatchFg.Render("bob"))
}
