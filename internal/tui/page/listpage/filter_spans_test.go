// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// TestFilterSpans_TextGrammar covers the pages whose `/` buffer is
// text only (silences, receivers): every mode reports the ranges the
// renderer paints, and an empty buffer reports no reporter at all.
func TestFilterSpans_TextGrammar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		filter string
		cell   string
		want   [][2]int
	}{
		{"empty buffer has no reporter", "", "high cpu", nil},
		{"substring", "cpu", "high cpu", [][2]int{{5, 8}}},
		{"fuzzy", "~hcp", "high cpu", [][2]int{{0, 1}, {5, 7}}},
		{"literal", `\cpu`, "high cpu", [][2]int{{5, 8}}},
		{"regex", "^high.*", "high cpu", [][2]int{{0, 8}}},
		{"a regex that does not compile falls back to substring", "[oops", "an [oops here", [][2]int{{3, 8}}},
		{"a label selector reads as text here", "team=platform", "team=platform", [][2]int{{0, 13}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := listpage.Base{Filter: tc.filter}
			spans := b.FilterSpans()
			if tc.filter == "" {
				require.Nil(t, spans)
				return
			}
			require.NotNil(t, spans)
			require.Equal(t, tc.want, spans(tc.cell))
		})
	}
}

// TestFilterSpans_LabelGrammar covers the pages that also accept a
// Prometheus label selector. A selector matches on label names the
// table does not render, so it paints nothing; a text buffer on the
// same page still paints.
func TestFilterSpans_LabelGrammar(t *testing.T) {
	t.Parallel()

	b := listpage.Base{Filter: "team=platform", FilterValidate: listpage.LabelFilterValidate}
	require.Nil(t, b.FilterSpans())

	b.Filter = "cpu"
	spans := b.FilterSpans()
	require.NotNil(t, spans)
	require.Equal(t, [][2]int{{5, 8}}, spans("high cpu"))
}

// TestFilterSpans_Expression pins the stand-down: an expression
// spreads its matching characters across terms the reporter cannot
// attribute, so it paints nothing rather than the wrong cells.
func TestFilterSpans_Expression(t *testing.T) {
	t.Parallel()

	b := listpage.Base{Filter: "cpu || mem", FilterValidate: listpage.LabelFilterValidate}
	require.Nil(t, b.FilterSpans())

	b.FilterValidate = nil
	spans := b.FilterSpans()
	require.NotNil(t, spans, "a page that does not read expressions still paints the buffer as text")
	require.Equal(t, [][2]int{{0, 10}}, spans("cpu || mem"))
}
