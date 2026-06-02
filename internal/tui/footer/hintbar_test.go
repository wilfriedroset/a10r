// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// fixtureTips is the curated list every hint-bar test feeds the
// constructor. Three entries is enough to exercise the modulo wrap
// without depending on whatever the production help.Tips() returns.
func fixtureTips() []help.Tip {
	return []help.Tip{
		{Key: "?", Text: "open help"},
		{Key: ":", Text: "command bar"},
		{Key: "/", Text: "filter"},
	}
}

func TestHintBar_DisabledByDefault(t *testing.T) {
	t.Parallel()

	// The zero value MUST behave as fully OFF — this is the project
	// memory's "no scouted features without explicit go" rule made
	// load-bearing in code: any path that constructs a HintBar
	// without explicit opt-in (a forgotten config field, a bug in
	// the wiring layer) renders nothing and fires no tick.
	var zero HintBar
	require.False(t, zero.Enabled())
	require.Nil(t, zero.Start(), "disabled bar must not schedule a tick")
	require.Empty(t, zero.Render(testutil.LoadStyles(t)),
		"disabled bar must render as empty so the footer collapses")

	// Explicit Enabled:false runs the same short-circuit.
	bar := NewHintBar(HintBarOptions{Enabled: false, Tips: fixtureTips()})
	require.False(t, bar.Enabled())
	require.Nil(t, bar.Start())
	require.Empty(t, bar.Render(testutil.LoadStyles(t)))
}

func TestHintBar_EmptyTipsShortCircuits(t *testing.T) {
	t.Parallel()

	// An explicit empty catalogue must render no bar even with
	// Enabled:true. Guards against a future tips.go mistake or a
	// test injecting an empty slice.
	bar := NewHintBar(HintBarOptions{Enabled: true, Tips: []help.Tip{}})
	require.False(t, bar.Enabled(),
		"empty tips slice must collapse to disabled")
	require.Nil(t, bar.Start())
	require.Empty(t, bar.Render(testutil.LoadStyles(t)))
}

func TestHintBar_DisabledIgnoresTicks(t *testing.T) {
	t.Parallel()

	// A stale tick arriving after a (hypothetical) runtime disable
	// must not crash and must not advance any cursor — Update is a
	// no-op for every input on a disabled bar.
	var bar HintBar
	out, cmd := bar.Update(hintBarTickMsg{generation: 0})
	require.Equal(t, bar, out)
	require.Nil(t, cmd)

	// Non-tick messages also no-op.
	out, cmd = bar.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.Equal(t, bar, out)
	require.Nil(t, cmd)
}

func TestHintBar_StartSchedulesTick(t *testing.T) {
	t.Parallel()

	bar := NewHintBar(HintBarOptions{
		Enabled:  true,
		Interval: 50 * time.Millisecond,
		Tips:     fixtureTips(),
	})
	require.True(t, bar.Enabled())

	cmd := bar.Start()
	require.NotNil(t, cmd, "enabled bar must schedule the first tick")

	// The first tip rendered before any tick fires must be the
	// catalogue's first entry — rotation starts at 0, advances on
	// tick.
	require.Equal(t, "?", bar.Current().Key)
}

func TestHintBar_TickRotatesCursor(t *testing.T) {
	t.Parallel()

	tips := fixtureTips()
	bar := NewHintBar(HintBarOptions{
		Enabled:  true,
		Interval: 50 * time.Millisecond,
		Tips:     tips,
	})

	// Drive the rotation manually by injecting hintBarTickMsg under
	// the live generation. tea.Tick scheduling is opaque in unit
	// tests; the deterministic equivalent is to feed the message
	// the Tick would have produced.
	cases := []string{":", "/", "?", ":", "/"}
	for i, want := range cases {
		next, cmd := bar.Update(hintBarTickMsg{generation: 0})
		require.NotNilf(t, cmd, "tick %d must schedule the next tick", i)
		require.Equalf(t, want, next.Current().Key,
			"tick %d advanced to wrong tip", i)
		bar = next
	}
}

func TestHintBar_StaleGenerationDropped(t *testing.T) {
	t.Parallel()

	// A tick stamped with an older generation must NOT advance the
	// cursor and must NOT reschedule — the active generation owns
	// the timer. This is the same defensive idiom Flash uses for
	// its auto-clear.
	bar := NewHintBar(HintBarOptions{
		Enabled:  true,
		Interval: 50 * time.Millisecond,
		Tips:     fixtureTips(),
	})
	bar.generation = 7

	out, cmd := bar.Update(hintBarTickMsg{generation: 6})
	require.Nil(t, cmd, "stale-generation tick must not reschedule")
	require.Equal(t, "?", out.Current().Key,
		"stale-generation tick must not advance the cursor")
}

func TestHintBar_CadenceOverride(t *testing.T) {
	t.Parallel()

	// Custom interval flows through; zero / negative falls back to
	// the package default. Both branches matter — a partial config
	// (tips: true alone, no interval) is the common case.
	custom := NewHintBar(HintBarOptions{
		Enabled:  true,
		Interval: 250 * time.Millisecond,
		Tips:     fixtureTips(),
	})
	require.Equal(t, 250*time.Millisecond, custom.Interval())

	zero := NewHintBar(HintBarOptions{
		Enabled: true,
		Tips:    fixtureTips(),
	})
	require.Equal(t, DefaultHintBarInterval, zero.Interval())

	negative := NewHintBar(HintBarOptions{
		Enabled:  true,
		Interval: -1 * time.Second,
		Tips:     fixtureTips(),
	})
	require.Equal(t, DefaultHintBarInterval, negative.Interval())
}

func TestHintBar_RenderShowsKeyAndText(t *testing.T) {
	t.Parallel()

	styles := testutil.LoadStyles(t)
	bar := NewHintBar(HintBarOptions{
		Enabled: true,
		Tips: []help.Tip{
			{Key: "?", Text: "open help"},
		},
	})

	out := bar.Render(styles)
	require.NotEmpty(t, out)
	plain := testutil.StripStyle(out)
	require.Contains(t, plain, "<?>", "key chip must use the angle-bracket form")
	require.Contains(t, plain, "open help")
}

func TestHintBar_LigatureSafeChip(t *testing.T) {
	t.Parallel()

	// Same rule as the help overlay: keys that would form a
	// programming-font ligature inside `<…>` (`-`, `=`, `<`, `>`)
	// render with square brackets so a tip about `-` doesn't turn
	// into an arrow glyph on Fira Code / JetBrains Mono.
	styles := testutil.LoadStyles(t)
	cases := []struct {
		key, want string
	}{
		{"-", "[-]"},
		{"=", "[=]"},
		{"<", "[<]"},
		{">", "[>]"},
		{"?", "<?>"},
		{"g", "<g>"},
	}
	for _, c := range cases {
		bar := NewHintBar(HintBarOptions{
			Enabled: true,
			Tips:    []help.Tip{{Key: c.key, Text: "x"}},
		})
		plain := testutil.StripStyle(bar.Render(styles))
		require.Containsf(t, plain, c.want,
			"key %q must render as chip %q (got %q)", c.key, c.want, plain)
	}
}

func TestHintBar_OwnsRoutesTickOnly(t *testing.T) {
	t.Parallel()

	bar := NewHintBar(HintBarOptions{
		Enabled: true,
		Tips:    fixtureTips(),
	})
	require.True(t, bar.Owns(hintBarTickMsg{}),
		"the bar must claim its own tick type")
	require.False(t, bar.Owns(tea.KeyPressMsg{}),
		"non-tick messages must route past the bar to the dispatcher")
	require.False(t, bar.Owns(tea.WindowSizeMsg{}))
}

func TestHintBar_DefaultTipsWhenNil(t *testing.T) {
	t.Parallel()

	// Nil Tips falls back to help.Tips() so the wiring layer
	// doesn't need to import help just to build the bar.
	bar := NewHintBar(HintBarOptions{Enabled: true})
	require.True(t, bar.Enabled(),
		"production help.Tips() must be non-empty so the default fallback enables the bar")
	require.NotEmpty(t, bar.Current().Key)
}
