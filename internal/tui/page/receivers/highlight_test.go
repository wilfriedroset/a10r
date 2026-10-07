// SPDX-License-Identifier: Apache-2.0

package receivers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// TestHighlight_PaintsRows asserts both row kinds and the layout:
// the cursor row keeps its own style across the match, the plain row
// takes the match colour, and neither render moves the text.
func TestHighlight_PaintsRows(t *testing.T) {
	t.Parallel()

	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops-page"}, {Name: "ops-mail"}}})
	plain := testutil.StripStyle(p.View(60, 10))

	require.True(t, p.SetFilter("ops"))
	p.recompute()
	out := p.View(60, 10)
	require.Equal(t, plain, testutil.StripStyle(out), "the match must not move the text")

	var cursor, row string
	for l := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.Contains(testutil.StripStyle(l), "ops-page"):
			row = l
		case strings.Contains(testutil.StripStyle(l), "ops-mail"):
			cursor = l
		}
	}
	require.NotEmpty(t, cursor)
	require.NotEmpty(t, row)
	require.Contains(t, cursor, format.Emphasis("ops"))
	require.Contains(t, row, p.styles.Table.MatchFg.Render("ops"))
}

// TestHighlight_KeepsColumnsPut is the layout contract: painting the
// match adds styling and nothing else, so the text and the column
// positions must come out byte for byte the same as the unfiltered
// render of the same rows. Every filter below keeps all three rows on
// purpose, because a narrowed view would lay out different columns
// and the comparison would prove nothing.
func TestHighlight_KeepsColumnsPut(t *testing.T) {
	t.Parallel()

	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
		{Name: "ops-page"}, {Name: "ops-mail"}, {Name: "ops-default"},
	}})
	plain := testutil.StripStyle(p.View(60, 10))

	for _, filter := range []string{"ops", "~ops", `\ops`, "o.*s"} {
		require.True(t, p.SetFilter(filter))
		p.recompute()
		require.Len(t, p.view, 3, "filter %q must keep every row", filter)
		require.Equal(t, plain, testutil.StripStyle(p.View(60, 10)),
			"filter %q moved the text", filter)
	}
}
