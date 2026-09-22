// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// seeded returns a Base whose filter is already set, which is the
// only way a buffer becomes a filter now that the field is
// unexported.
func seeded(t *testing.T, g filterexpr.Grammar, buffer string) *listpage.Base {
	t.Helper()
	b := &listpage.Base{Grammar: g}
	require.True(t, b.SetFilter(buffer), "seed buffer must compile: %q", buffer)
	return b
}

// TestBase_SetFilterRefusesAndKeepsThePrevious pins the live-filter
// contract at its new single writer: a buffer that does not compile
// leaves the rows on the last good classification, records why, and
// reports the refusal so the caller skips the recompute.
func TestBase_SetFilterRefusesAndKeepsThePrevious(t *testing.T) {
	t.Parallel()

	b := seeded(t, filterexpr.Grammar{}, "web")

	require.False(t, b.SetFilter("^web("))
	require.Equal(t, "web", b.FilterBuffer(), "a refused buffer never becomes the filter")
	require.EqualError(t, b.FilterErr, "regex: missing closing )")

	require.True(t, b.SetFilter("^web.*"))
	require.Equal(t, "^web.*", b.FilterBuffer())
	require.NoError(t, b.FilterErr, "the keystroke that fixes the buffer clears the error")
}

// TestBase_FilterReadsAgree proves the point of the unexported field:
// the buffer, the mode label, the spans and the predicate are four
// reads of one classification, so a page cannot ask one question of
// one grammar and another question of another.
func TestBase_FilterReadsAgree(t *testing.T) {
	t.Parallel()

	b := seeded(t, filterexpr.AlertGrammar, "team=platform")
	require.Equal(t, "team=platform", b.FilterBuffer())
	require.Empty(t, b.FilterMode(), "a plain selector shows no mode tag")
	require.Nil(t, b.FilterSpans(), "a selector matches on labels, not on the rendered text")
	require.False(t, b.FilterMatchAll())
	require.True(t, b.FilterMatch(filterexpr.Row{
		Labels:   map[string]string{"team": "platform"},
		Instance: filterexpr.Present,
	}))

	require.True(t, b.SetFilter("~hcp"))
	require.Equal(t, "fuzzy", b.FilterMode())
	spans := b.FilterSpans()
	require.NotNil(t, spans)
	require.Equal(t, [][2]int{{0, 1}, {5, 7}}, spans("high cpu"))
	require.True(t, b.FilterMatch(filterexpr.Row{Text: "high cpu", Instance: filterexpr.Present}))
}

// TestBase_FilterMatchAll pins the fast path: only the empty buffer
// lets a page hand its input slice straight to the view, and the zero
// value is that buffer.
func TestBase_FilterMatchAll(t *testing.T) {
	t.Parallel()

	var zero listpage.Base
	require.True(t, zero.FilterMatchAll())
	require.Empty(t, zero.FilterBuffer())
	require.Nil(t, zero.FilterSpans())

	b := seeded(t, filterexpr.Grammar{}, "cpu")
	require.False(t, b.FilterMatchAll())
	require.True(t, b.SetFilter(""))
	require.True(t, b.FilterMatchAll())
}

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
		{"a lone regex meta stays a substring", "[oops", "an [oops here", [][2]int{{3, 8}}},
		{"a label selector reads as text here", "team=platform", "team=platform", [][2]int{{0, 13}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spans := seeded(t, filterexpr.Grammar{}, tc.filter).FilterSpans()
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

	b := seeded(t, filterexpr.AlertGrammar, "team=platform")
	require.Nil(t, b.FilterSpans())

	require.True(t, b.SetFilter("cpu"))
	spans := b.FilterSpans()
	require.NotNil(t, spans)
	require.Equal(t, [][2]int{{5, 8}}, spans("high cpu"))
}

// TestFilterSpans_Expression pins the stand-down: an expression
// spreads its matching characters across terms the reporter cannot
// attribute, so it paints nothing rather than the wrong cells.
func TestFilterSpans_Expression(t *testing.T) {
	t.Parallel()

	require.Nil(t, seeded(t, filterexpr.AlertGrammar, "cpu || mem").FilterSpans())
	require.Nil(t, seeded(t, filterexpr.Grammar{Expressions: true}, "cpu || mem").FilterSpans(),
		"the expression rung stands the reporter down on its own")

	spans := seeded(t, filterexpr.Grammar{}, "cpu || mem").FilterSpans()
	require.NotNil(t, spans, "a page that does not read expressions still paints the buffer as text")
	require.Equal(t, [][2]int{{0, 10}}, spans("cpu || mem"))
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

	b.Grammar = filterexpr.AlertGrammar
	require.NoError(t, b.ValidateFilter("cluster_id=99"))
	require.Error(t, b.ValidateFilter(`a=~"("`))
}

// TestBase_GrammarTellsTheHalvesApart proves the two booleans are
// independent, which the single injected validator could not express:
// a page can read label selectors without also reading expressions.
func TestBase_GrammarTellsTheHalvesApart(t *testing.T) {
	t.Parallel()

	selectors := seeded(t, filterexpr.Grammar{Selectors: true}, "team=platform")
	require.False(t, selectors.FilterReadsExpr())
	require.Nil(t, selectors.FilterSpans(), "a selector paints nothing on a page that reads selectors")

	expressions := &listpage.Base{Grammar: filterexpr.Grammar{Expressions: true}}
	require.True(t, expressions.FilterReadsExpr())
	require.EqualError(t, expressions.ValidateFilter("team=platform ||"), "expr: missing term after ||")
}
