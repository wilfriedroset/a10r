// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// TestBase_ValidateFilterTextGrammar walks the default grammar a page
// gets when its constructor injects no FilterValidate.
func TestBase_ValidateFilterTextGrammar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{name: "empty buffer is valid", input: ""},
		{name: "substring buffer is valid", input: "web"},
		{name: "fuzzy sigil is valid", input: "~web"},
		{name: "compiling regex is valid", input: "^web.*"},
		{name: "label matcher is not a regex here", input: "cluster_id=99"},
		{
			name:    "half-typed group",
			input:   "^web(",
			wantMsg: "regex: missing closing )",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := (&listpage.Base{}).ValidateFilter(tc.input)
			if tc.wantMsg == "" {
				require.NoError(t, got)
				return
			}
			require.EqualError(t, got, tc.wantMsg)
		})
	}
}

func TestLabelFilterValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{name: "valid label matcher", input: "cluster_id=~9.*"},
		{name: "bare word falls through to text mode", input: "web"},
		{name: "valid text regex", input: "^web.*"},
		{
			name:    "uncompilable matcher regex reports the matcher kind",
			input:   `a=~"("`,
			wantMsg: `matcher: compile regex "(": missing closing )`,
		},
		{
			name:    "uncompilable text regex still reports the regex kind",
			input:   "^web(",
			wantMsg: "regex: missing closing )",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := listpage.LabelFilterValidate(tc.input)
			if tc.wantMsg == "" {
				require.NoError(t, got)
				return
			}
			require.EqualError(t, got, tc.wantMsg)
		})
	}
}

// TestBase_ValidateFilterIsNilInterface guards the typed-nil trap: the
// app consumes ValidateFilter through an `error`-returning seam, so a
// valid buffer must yield an untyped nil. A concrete pointer return
// would box into a non-nil error and reject every buffer.
func TestBase_ValidateFilterIsNilInterface(t *testing.T) {
	t.Parallel()

	b := &listpage.Base{}
	require.NoError(t, b.ValidateFilter("web"))
	require.Error(t, b.ValidateFilter("^web("))

	b.FilterValidate = listpage.LabelFilterValidate
	require.NoError(t, b.ValidateFilter("cluster_id=99"))
	require.Error(t, b.ValidateFilter(`a=~"("`))
}

// TestBase_FilterError pins the reason the app title renders.
func TestBase_FilterError(t *testing.T) {
	t.Parallel()

	b := &listpage.Base{Recompute: func() {}}
	require.NoError(t, b.FilterError(), "a page with a usable buffer reports no reason")

	b.HandleFilterPrompt(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "^web("})
	require.EqualError(t, b.FilterError(), "regex: missing closing )")
}
