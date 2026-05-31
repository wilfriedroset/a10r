// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func TestBase_RenderListFrame(t *testing.T) {
	t.Parallel()

	t.Run("non-positive dimensions render nothing", func(t *testing.T) {
		t.Parallel()
		b := &listpage.Base{}
		require.Empty(t, b.RenderListFrame(listpage.ListFrame{Width: 0, Height: 10, Count: 3}))
		require.Empty(t, b.RenderListFrame(listpage.ListFrame{Width: 80, Height: 0, Count: 3}))
	})

	t.Run("empty list shows the empty-state body, skips header and rows", func(t *testing.T) {
		t.Parallel()
		b := &listpage.Base{}
		called := false
		out := testutil.StripStyle(b.RenderListFrame(listpage.ListFrame{
			Width: 80, Height: 10, Count: 0,
			EmptyState: func() string { return "nothing here" },
			Header:     func(int) string { called = true; return "HDR" },
			Rows:       func(int, int) string { called = true; return "rows" },
		}))
		require.Contains(t, out, "nothing here")
		require.NotContains(t, out, "HDR")
		require.False(t, called, "header/rows must not run on the empty path")
	})

	t.Run("populated list stacks header above rows, skips empty-state", func(t *testing.T) {
		t.Parallel()
		b := &listpage.Base{}
		var gotMaxRows int
		out := testutil.StripStyle(b.RenderListFrame(listpage.ListFrame{
			Width: 80, Height: 10, Count: 2,
			EmptyState: func() string { return "EMPTY" },
			Header:     func(int) string { return "HDR" },
			Rows: func(_, maxRows int) string {
				gotMaxRows = maxRows
				return "r0\nr1"
			},
		}))
		require.Equal(t, []string{"HDR", "r0", "r1"}, trimmedLines(out))
		require.NotContains(t, out, "EMPTY")
		require.Equal(t, 9, gotMaxRows, "rows get height-1 when no band is shown")
	})

	t.Run("error band is prepended and steals a row from the body height", func(t *testing.T) {
		t.Parallel()
		b := &listpage.Base{
			Scope:         listpage.ScopeAll,
			BackendHealth: map[string]listpage.BackendHealth{"prod": {Detail: "boom"}},
		}
		var gotMaxRows int
		out := testutil.StripStyle(b.RenderListFrame(listpage.ListFrame{
			Width: 80, Height: 10, Count: 1,
			Now:        frozenNow,
			EmptyState: func() string { return "EMPTY" },
			Header:     func(int) string { return "HDR" },
			Rows: func(_, maxRows int) string {
				gotMaxRows = maxRows
				return "r0"
			},
		}))
		lines := trimmedLines(out)
		require.Contains(t, lines[0], "boom", "band sits on the first line")
		require.Equal(t, []string{"HDR", "r0"}, lines[1:])
		require.Equal(t, 8, gotMaxRows, "band costs the body one row (height-1-1)")
	})
}

// trimmedLines splits s into lines with each line's right-padding
// stripped, so assertions compare content not the Pane/Wrap fill.
func trimmedLines(s string) []string {
	out := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range out {
		out[i] = strings.TrimRight(line, " ")
	}
	return out
}
