// SPDX-License-Identifier: Apache-2.0

package selfreport

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func keyPress(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func TestPage_SatisfiesTheAppPageContract(t *testing.T) {
	t.Parallel()

	var _ app.Page = New(Options{Title: "info", Render: func() string { return "" }})
}

func TestPage_RendersTheSuppliedBody(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Title:  "info",
		Render: func() string { return "first\nsecond" },
	})

	out := testutil.StripStyle(p.View(40, 10))
	require.Contains(t, out, "first")
	require.Contains(t, out, "second")
	require.Equal(t, "info", p.Title())
	require.Equal(t, "info", p.Crumb())
}

// The page reflects the live process, so `r` must pick up a body the
// renderer now produces differently rather than replaying the text
// captured at push time.
func TestPage_RefreshReRendersTheBody(t *testing.T) {
	t.Parallel()

	calls := 0
	p := New(Options{
		Title: "info",
		Render: func() string {
			calls++
			return "call " + strconv.Itoa(calls)
		},
	})
	require.Contains(t, testutil.StripStyle(p.View(40, 10)), "call 1")

	p.Update(keyPress("r"))
	require.Contains(t, testutil.StripStyle(p.View(40, 10)), "call 2")
}

func TestPage_ScrollsWithTheSharedDetailMotions(t *testing.T) {
	t.Parallel()

	body := make([]string, 0, 50)
	for i := range 50 {
		body = append(body, "line "+strconv.Itoa(i))
	}
	p := New(Options{
		Title:  "info",
		Render: func() string { return strings.Join(body, "\n") },
	})
	require.Contains(t, testutil.StripStyle(p.View(40, 5)), "line 0")

	p.Update(keyPress("j"))
	out := testutil.StripStyle(p.View(40, 5))
	require.NotContains(t, out, "line 0")
	require.Contains(t, out, "line 1")
}

// Nothing on the page reaches the network or the filesystem after
// construction, so a zero-size frame must not panic the renderer.
func TestPage_ZeroSizeFrameRendersNothing(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Title:  "info",
		Render: func() string { return "body" },
	})
	require.Empty(t, p.View(0, 0))
}

// The `gg` chord is registered as a chord at keys.LayerTable, which
// every page sees, so any page can receive GoToFirstRowMsg. A detail
// page that skips the sideband router answers `G` but not `gg`.
func TestPage_GoToFirstRowScrollsHome(t *testing.T) {
	t.Parallel()

	body := make([]string, 0, 50)
	for i := range 50 {
		body = append(body, "line "+strconv.Itoa(i))
	}
	p := New(Options{
		Title:  "info",
		Render: func() string { return strings.Join(body, "\n") },
	})
	p.Update(keyPress("G"))
	require.NotContains(t, testutil.StripStyle(p.View(40, 5)), "line 0")

	p.Update(app.GoToFirstRowMsg{})
	require.Contains(t, testutil.StripStyle(p.View(40, 5)), "line 0")
}

// An anchor is how a long report stays usable without a search: the
// `:config` page's warnings sit below however many sources the host
// has, so `w` must land on them whatever that number is.
func TestPage_AnchorJumpsToTheNamedLine(t *testing.T) {
	t.Parallel()

	body := []string{"sources (2):", "  base /a", "  drop-in /b", "", "warnings (1):", "  something"}
	p := New(Options{
		Title:  "config",
		Render: func() string { return strings.Join(body, "\n") },
		Anchors: []Anchor{
			{Key: "p", Description: "sources", Prefix: "sources ("},
			{Key: "w", Description: "warnings", Prefix: "warnings ("},
		},
	})

	p.Update(keyPress("w"))
	out := testutil.StripStyle(p.View(40, 2))
	require.Contains(t, out, "warnings (1):")
	require.NotContains(t, out, "sources (2):")

	p.Update(keyPress("p"))
	require.Contains(t, testutil.StripStyle(p.View(40, 2)), "sources (2):")
}

// The anchors join the hint strip, so the operator learns the two
// jumps without opening the help overlay.
func TestPage_AnchorsAppearInTheBindings(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Title:   "config",
		Render:  func() string { return "sources (0):" },
		Anchors: []Anchor{{Key: "p", Description: "sources", Prefix: "sources ("}},
	})

	bindings := p.Bindings()
	keys := make([]string, 0, len(bindings))
	for _, b := range bindings {
		keys = append(keys, b.Key)
	}
	require.Contains(t, keys, "p")
}

// A report that never grew the anchored section must not scroll
// somewhere arbitrary: no matching line means the key does nothing.
func TestPage_AnchorWithNoMatchingLineDoesNotScroll(t *testing.T) {
	t.Parallel()

	body := make([]string, 0, 30)
	for i := range 30 {
		body = append(body, "line "+strconv.Itoa(i))
	}
	p := New(Options{
		Title:   "config",
		Render:  func() string { return strings.Join(body, "\n") },
		Anchors: []Anchor{{Key: "w", Description: "warnings", Prefix: "warnings ("}},
	})

	p.Update(keyPress("w"))
	require.Contains(t, testutil.StripStyle(p.View(40, 3)), "line 0")
}
