// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

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
				Filter:    tc.seed.filter,
				PreFilter: tc.seed.preFilter,
				Recompute: func() { calls++ },
			}
			b.HandleFilterPrompt(tc.msg)
			require.Equal(t, tc.want.filter, b.Filter, "filter mismatch")
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
