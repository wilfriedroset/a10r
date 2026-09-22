// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestBase_HandleFilterPrompt(t *testing.T) {
	t.Parallel()

	type fixture struct {
		filter    string
		preFilter *string
	}

	type wantState struct {
		filter       string
		preFilterNil bool
		preFilterVal string
		recomputes   int
	}

	str := func(s string) *string { return &s }

	cases := []struct {
		name string
		seed fixture
		msg  any
		want wantState
	}{
		{
			name: "opened snapshots and clears non-empty filter",
			seed: fixture{filter: "warning"},
			msg:  footer.PromptOpenedMsg{Mode: footer.PromptFilter},
			want: wantState{filter: "", preFilterVal: "warning", recomputes: 1},
		},
		{
			name: "opened on empty filter snapshots empty without recompute",
			seed: fixture{filter: ""},
			msg:  footer.PromptOpenedMsg{Mode: footer.PromptFilter},
			want: wantState{filter: "", preFilterVal: "", recomputes: 0},
		},
		{
			name: "opened ignores non-filter modes",
			seed: fixture{filter: "x"},
			msg:  footer.PromptOpenedMsg{Mode: footer.PromptCommand},
			want: wantState{filter: "x", preFilterNil: true, recomputes: 0},
		},
		{
			name: "changed applies live and recomputes",
			seed: fixture{filter: "", preFilter: str("")},
			msg:  footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "warn"},
			want: wantState{filter: "warn", preFilterVal: "", recomputes: 1},
		},
		{
			name: "changed ignores command mode",
			seed: fixture{filter: "x"},
			msg:  footer.PromptChangedMsg{Mode: footer.PromptCommand, Value: "y"},
			want: wantState{filter: "x", preFilterNil: true, recomputes: 0},
		},
		{
			name: "submitted commits value and drops snapshot",
			seed: fixture{filter: "draft", preFilter: str("original")},
			msg:  footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "final"},
			want: wantState{filter: "final", preFilterNil: true, recomputes: 1},
		},
		{
			name: "submitted with empty value clears filter",
			seed: fixture{filter: "stale", preFilter: str("orig")},
			msg:  footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: ""},
			want: wantState{filter: "", preFilterNil: true, recomputes: 1},
		},
		{
			name: "submitted ignores command mode",
			seed: fixture{filter: "x", preFilter: str("y")},
			msg:  footer.PromptSubmittedMsg{Mode: footer.PromptCommand, Value: "z"},
			want: wantState{filter: "x", preFilterVal: "y", recomputes: 0},
		},
		{
			name: "cancelled restores snapshot and clears preFilter",
			seed: fixture{filter: "typed", preFilter: str("snapshot")},
			msg:  footer.PromptCancelledMsg{Mode: footer.PromptFilter},
			want: wantState{filter: "snapshot", preFilterNil: true, recomputes: 1},
		},
		{
			name: "cancelled without preFilter is a no-op",
			seed: fixture{filter: "typed"},
			msg:  footer.PromptCancelledMsg{Mode: footer.PromptFilter},
			want: wantState{filter: "typed", preFilterNil: true, recomputes: 0},
		},
		{
			name: "cancelled ignores command mode",
			seed: fixture{filter: "x", preFilter: str("y")},
			msg:  footer.PromptCancelledMsg{Mode: footer.PromptCommand},
			want: wantState{filter: "x", preFilterVal: "y", recomputes: 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			b := &listpage.Base{
				PreFilter: tc.seed.preFilter,
				Recompute: func() { calls++ },
			}
			require.True(t, b.SetFilter(tc.seed.filter))
			b.HandleFilterPrompt(tc.msg)
			require.Equal(t, tc.want.filter, b.FilterBuffer(), "filter mismatch")
			require.Equal(t, tc.want.recomputes, calls, "recompute call count")
			if tc.want.preFilterNil {
				require.Nil(t, b.PreFilter, "preFilter should be nil")
			} else {
				require.NotNil(t, b.PreFilter, "preFilter should be set")
				require.Equal(t, tc.want.preFilterVal, *b.PreFilter)
			}
		})
	}
}

func TestBase_HandleFilterPrompt_PanicsWithoutRecompute(t *testing.T) {
	t.Parallel()
	b := &listpage.Base{}
	require.PanicsWithValue(t,
		"listpage.Base.HandleFilterPrompt: Recompute callback not wired by page constructor",
		func() { b.HandleFilterPrompt(footer.PromptOpenedMsg{Mode: footer.PromptFilter}) },
	)
}

// TestBase_HandleFilterPrompt_InvalidBufferFreezesRows pins the
// live-filter contract for malformed input: while the buffer cannot
// compile the row set must not move, and the next keystroke that makes
// it valid again recomputes and clears the error.
func TestBase_HandleFilterPrompt_InvalidBufferFreezesRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		bad     func(string) tea.Msg
		good    func(string) tea.Msg
		wantPre bool
	}{
		{
			name:    "changed",
			bad:     func(v string) tea.Msg { return footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: v} },
			good:    func(v string) tea.Msg { return footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: v} },
			wantPre: true,
		},
		{
			name:    "submitted",
			bad:     func(v string) tea.Msg { return footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: v} },
			good:    func(v string) tea.Msg { return footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: v} },
			wantPre: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			snap := "seed"
			b := &listpage.Base{
				PreFilter: &snap,
				Recompute: func() { calls++ },
			}
			require.True(t, b.SetFilter("seed"))

			b.HandleFilterPrompt(tc.bad("^web("))
			require.Equal(t, "seed", b.FilterBuffer(), "an uncompilable buffer must not become the filter")
			require.Zero(t, calls, "an uncompilable buffer must not recompute")
			require.EqualError(t, b.FilterErr, "regex: missing closing )")
			require.NotNil(t, b.PreFilter, "the pre-prompt snapshot survives a rejected buffer")

			b.HandleFilterPrompt(tc.good("^web.*"))
			require.Equal(t, "^web.*", b.FilterBuffer())
			require.Equal(t, 1, calls)
			require.NoError(t, b.FilterErr, "a buffer that compiles again clears the error")
			require.Equal(t, tc.wantPre, b.PreFilter != nil)
		})
	}
}

// TestBase_HandleFilterPrompt_OpenAndCancelClearFilterErr pins that a
// stale error never outlives the prompt session that produced it.
func TestBase_HandleFilterPrompt_OpenAndCancelClearFilterErr(t *testing.T) {
	t.Parallel()

	for _, msg := range []tea.Msg{
		footer.PromptOpenedMsg{Mode: footer.PromptFilter},
		footer.PromptCancelledMsg{Mode: footer.PromptFilter},
	} {
		b := &listpage.Base{Recompute: func() {}}
		b.HandleFilterPrompt(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "^web("})
		require.Error(t, b.FilterErr)
		b.HandleFilterPrompt(msg)
		require.NoError(t, b.FilterErr)
	}
}

// TestBase_HandleFilterPrompt_LabelGrammarRejectsMatcherRegex covers
// the alerts / group-detail wiring: a label selector whose regex will
// not compile is a matcher-kind error, not a text search.
func TestBase_HandleFilterPrompt_LabelGrammarRejectsMatcherRegex(t *testing.T) {
	t.Parallel()

	calls := 0
	b := &listpage.Base{
		Recompute: func() { calls++ },
		Grammar:   filterexpr.AlertGrammar,
	}
	b.HandleFilterPrompt(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: `a=~"("`})
	require.Zero(t, calls)
	require.Empty(t, b.FilterBuffer())
	require.EqualError(t, b.FilterErr, `matcher: compile regex "(": missing closing )`)
}

// TestBase_FilterError pins the reason the app title renders.
func TestBase_FilterError(t *testing.T) {
	t.Parallel()

	b := &listpage.Base{Recompute: func() {}}
	require.NoError(t, b.FilterError(), "a page with a usable buffer reports no reason")

	b.HandleFilterPrompt(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "^web("})
	require.EqualError(t, b.FilterError(), "regex: missing closing )")
}
