// SPDX-License-Identifier: Apache-2.0

package filterexpr_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
)

// selectorGrammar reads label selectors and no expressions. No page
// declares it today; the table uses it to prove the two halves of a
// Grammar are independent.
var selectorGrammar = filterexpr.Grammar{Selectors: true}

// probeRow carries a label the selector grammar can see and a text
// composite that shares nothing with it, so a buffer that matches the
// row proves which grammar ran.
func probeRow() filterexpr.Row {
	return filterexpr.Row{
		Labels:   map[string]string{"severity": "critical"},
		Text:     "highcpu",
		State:    "active",
		Instance: filterexpr.Present,
	}
}

// TestCompile_GrammarWins pins the precedence: the expression parser
// gets first refusal on a page that reads expressions, a buffer it
// does not own is judged as a selector on a page that reads
// selectors, and the rest is the five-mode text path. The same buffer
// under a narrower grammar falls through to the next language.
func TestCompile_GrammarWins(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		buffer  string
		grammar filterexpr.Grammar
		want    bool
	}{
		{"selector reads labels", "severity=critical", selectorGrammar, true},
		{"selector reads labels under the alert grammar", "severity=critical", filterexpr.AlertGrammar, true},
		{"text grammar reads the selector as text", "severity=critical", filterexpr.Grammar{}, false},
		{"expression reads labels", "severity=critical || nope=1", filterexpr.AlertGrammar, true},
		{"selector grammar cannot read an expression", "severity=critical || nope=1", selectorGrammar, false},
		{"text term", "highcpu", filterexpr.AlertGrammar, true},
		{"text term on a text grammar", "highcpu", filterexpr.Grammar{}, true},
		{"typed term hands the buffer to the parser", "state=active", filterexpr.AlertGrammar, true},
		{"a selector grammar reads a typed term as a label the row lacks", "state=active", selectorGrammar, false},
		{"negation is an expression signal", "!severity=info", filterexpr.AlertGrammar, true},
		{"an and chain stays a selector", "severity=critical,nope!=1", filterexpr.AlertGrammar, true},
		{"a lone paren stays on the text path", "(highcpu", filterexpr.AlertGrammar, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := filterexpr.Compile(tc.buffer, tc.grammar)
			require.NoError(t, err)
			require.Equal(t, tc.want, c.Match(probeRow()))
			require.Equal(t, tc.buffer, c.Buffer())
		})
	}
}

// TestCompile_ErrorText pins the rejection message for each grammar:
// the prefix names the language that refused the buffer, so the title
// tag, the Enter flash and the `:alerts --filter` rejection all read
// the same.
func TestCompile_ErrorText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		buffer  string
		grammar filterexpr.Grammar
		wantMsg string
	}{
		{name: "empty buffer is valid", buffer: "", grammar: filterexpr.AlertGrammar},
		{name: "substring buffer is valid", buffer: "web", grammar: filterexpr.Grammar{}},
		{name: "fuzzy sigil is valid", buffer: "~web", grammar: filterexpr.Grammar{}},
		{name: "compiling regex is valid", buffer: "^web.*", grammar: filterexpr.Grammar{}},
		{name: "valid label matcher", buffer: "cluster_id=~9.*", grammar: filterexpr.AlertGrammar},
		{name: "valid boolean expression", buffer: "team=platform || severity=warning", grammar: filterexpr.AlertGrammar},
		{
			name:    "half-typed group on a text page reports the regex kind",
			buffer:  "^web(",
			grammar: filterexpr.Grammar{},
			wantMsg: "regex: missing closing )",
		},
		{
			name:    "half-typed group on an alert page still reports the regex kind",
			buffer:  "^web(",
			grammar: filterexpr.AlertGrammar,
			wantMsg: "regex: missing closing )",
		},
		{
			name:    "uncompilable matcher regex reports the matcher kind",
			buffer:  `a=~"("`,
			grammar: filterexpr.AlertGrammar,
			wantMsg: `matcher: compile regex "(": missing closing )`,
		},
		{
			name:    "unbalanced group reports the expr kind",
			buffer:  "!(team=platform",
			grammar: filterexpr.AlertGrammar,
			wantMsg: "expr: unbalanced (",
		},
		{
			name:    "dangling or reports the expr kind",
			buffer:  "team=platform ||",
			grammar: filterexpr.AlertGrammar,
			wantMsg: "expr: missing term after ||",
		},
		{
			name:    "dangling not reports the expr kind",
			buffer:  "team=platform && !",
			grammar: filterexpr.AlertGrammar,
			wantMsg: "expr: empty term after !",
		},
		{
			name:    "bad duration reports the expr kind",
			buffer:  "age<2x",
			grammar: filterexpr.AlertGrammar,
			wantMsg: `expr: bad duration "2x"`,
		},
		{
			name:    "unbalanced group with an and reports the expr kind",
			buffer:  "(a=1 && b=2",
			grammar: filterexpr.AlertGrammar,
			wantMsg: "expr: unbalanced (",
		},
		{
			name:    "uncompilable regex inside a term stays one line",
			buffer:  "severity=warning || /(/",
			grammar: filterexpr.AlertGrammar,
			wantMsg: "expr: missing closing )",
		},
		{
			name:    "a text page never reaches the expression parser",
			buffer:  "team=platform ||",
			grammar: filterexpr.Grammar{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := filterexpr.Compile(tc.buffer, tc.grammar)
			if tc.wantMsg == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantMsg)
		})
	}
}

// TestCompile_RejectedBufferStaysUsable pins what a refusal leaves
// behind, for each of the three grammars that can refuse: the buffer
// comes back as the text term it would have been, which is the
// fallback today's ladder reaches when a grammar declines. The rows
// stay live while the user types, and the chrome reports the error
// separately.
func TestCompile_RejectedBufferStaysUsable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		buffer  string
		grammar filterexpr.Grammar
		wantMsg string
		cell    string
		want    [][2]int
	}{
		{
			name:    "an uncompilable regex",
			buffer:  "^web(",
			grammar: filterexpr.Grammar{},
			wantMsg: "regex: missing closing )",
			cell:    "a ^web( here",
			want:    [][2]int{{2, 7}},
		},
		{
			name:    "an expression the parser owns but cannot read",
			buffer:  "team=platform ||",
			grammar: filterexpr.AlertGrammar,
			wantMsg: "expr: missing term after ||",
			cell:    "team=platform || x",
			want:    [][2]int{{0, 16}},
		},
		{
			// The old ladder judged this one as a selector, because
			// LabelPredicate reads it as the name `(a` with the value
			// `1 && b=2`. The expression refusal now stops the walk
			// before that rung. The prompt refuses the buffer either
			// way, so the reading only decides what a caller that
			// ignores the error sees.
			name:    "a broken expression that a selector could still read",
			buffer:  "(a=1 && b=2",
			grammar: filterexpr.AlertGrammar,
			wantMsg: "expr: unbalanced (",
			cell:    "x (a=1 && b=2",
			want:    [][2]int{{2, 13}},
		},
		{
			name:    "a selector with an uncompilable value",
			buffer:  `a=~"("`,
			grammar: selectorGrammar,
			wantMsg: `matcher: compile regex "(": missing closing )`,
			cell:    `a=~"(" here`,
			want:    [][2]int{{0, 6}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := filterexpr.Compile(tc.buffer, tc.grammar)
			require.EqualError(t, err, tc.wantMsg)
			require.True(t, c.Match(filterexpr.Row{Text: tc.cell, Instance: filterexpr.Present}),
				"a rejected buffer still filters as text")
			require.False(t, c.MatchAll())
			spans := c.Spans()
			require.NotNil(t, spans, "a rejected buffer still paints what it matched")
			require.Equal(t, tc.want, spans(tc.cell))
		})
	}
}

// TestCompiled_Spans covers the reporter the row renderer hands to
// format.Highlighter: every text mode reports the ranges it painted,
// and the four non-painting buffers report no reporter at all.
func TestCompiled_Spans(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		buffer  string
		grammar filterexpr.Grammar
		cell    string
		want    [][2]int
	}{
		{"empty buffer", "", filterexpr.AlertGrammar, "high cpu", nil},
		{"bare fuzzy sigil", "~", filterexpr.Grammar{}, "high cpu", nil},
		{"bare literal sigil", `\`, filterexpr.Grammar{}, "high cpu", nil},
		{"selector", "team=platform", filterexpr.AlertGrammar, "team=platform", nil},
		{"expression", "cpu || mem", filterexpr.AlertGrammar, "cpu || mem", nil},
		{"substring", "cpu", filterexpr.Grammar{}, "high cpu", [][2]int{{5, 8}}},
		{"fuzzy", "~hcp", filterexpr.Grammar{}, "high cpu", [][2]int{{0, 1}, {5, 7}}},
		{"literal", `\cpu`, filterexpr.Grammar{}, "high cpu", [][2]int{{5, 8}}},
		{"regex", "^high.*", filterexpr.Grammar{}, "high cpu", [][2]int{{0, 8}}},
		{"a selector reads as text on a text page", "team=platform", filterexpr.Grammar{}, "team=platform", [][2]int{{0, 13}}},
		{"an expression reads as text on a text page", "cpu || mem", filterexpr.Grammar{}, "cpu || mem", [][2]int{{0, 10}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := filterexpr.Compile(tc.buffer, tc.grammar)
			require.NoError(t, err)
			spans := c.Spans()
			if tc.want == nil {
				require.Nil(t, spans)
				return
			}
			require.NotNil(t, spans)
			require.Equal(t, tc.want, spans(tc.cell))
		})
	}
}

// TestCompiled_ModeLabel pins the title's mode tag without its
// brackets. Substring is the default and stays quiet.
func TestCompiled_ModeLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		buffer  string
		grammar filterexpr.Grammar
		want    string
	}{
		{"empty buffer", "", filterexpr.AlertGrammar, ""},
		{"substring", "web", filterexpr.Grammar{}, ""},
		{"fuzzy", "~web", filterexpr.Grammar{}, "fuzzy"},
		{"literal", `\web.*`, filterexpr.Grammar{}, "literal"},
		{"regex", "^web.*", filterexpr.Grammar{}, "regex"},
		{"expression", "web || api", filterexpr.AlertGrammar, "expr"},
		{"an expression on a text page keeps its five-mode label", "web || api", filterexpr.Grammar{}, ""},
		{"a plain selector stays quiet", "team=platform", filterexpr.AlertGrammar, ""},
		{"a regex-valued selector keeps the five-mode label", "team=~pl.*", filterexpr.AlertGrammar, "regex"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := filterexpr.Compile(tc.buffer, tc.grammar)
			require.NoError(t, err)
			require.Equal(t, tc.want, c.ModeLabel())
		})
	}
}

// TestCompiled_MatchAll pins the fast path the recompute takes to
// hand its input slice straight back: only the empty buffer keeps
// every row, and the zero value is that buffer.
func TestCompiled_MatchAll(t *testing.T) {
	t.Parallel()

	var zero filterexpr.Compiled
	require.True(t, zero.MatchAll())
	require.True(t, zero.Match(probeRow()))
	require.Nil(t, zero.Spans())
	require.Empty(t, zero.ModeLabel())
	require.Empty(t, zero.Buffer())

	empty, err := filterexpr.Compile("", filterexpr.AlertGrammar)
	require.NoError(t, err)
	require.True(t, empty.MatchAll())

	for _, buffer := range []string{" ", "~", "web", "team=platform", "web || api"} {
		c, err := filterexpr.Compile(buffer, filterexpr.AlertGrammar)
		require.NoError(t, err)
		require.False(t, c.MatchAll(), "a buffer with a term keeps no row for free: %q", buffer)
	}
}

// TestCompiled_ReadsAgree is the invariant the four old
// classification sites could not state: the spans, the mode label and
// the predicate are reads of one value, so they cannot disagree.
func TestCompiled_ReadsAgree(t *testing.T) {
	t.Parallel()

	buffers := []string{
		"", " ", "~", `\`, "web", "~web", `\web`, "^web.*", "[oops",
		"team=platform", "team=~pl.*", "web || api", "count>=5", "!severity=info",
	}
	grammars := []filterexpr.Grammar{{}, selectorGrammar, filterexpr.AlertGrammar}

	for _, buffer := range buffers {
		for _, g := range grammars {
			c, _ := filterexpr.Compile(buffer, g)
			if c.ModeLabel() == "expr" {
				require.Nil(t, c.Spans(),
					"an expression spreads its characters across terms the reporter cannot attribute: %q", buffer)
			}
			if c.MatchAll() {
				require.Nil(t, c.Spans(), "a buffer that keeps every row paints nothing: %q", buffer)
			}
			if c.Spans() != nil {
				require.NotEmpty(t, c.Buffer(), "an empty buffer paints nothing")
				require.NotEqual(t, "expr", c.ModeLabel(), "a painting buffer is owned by a text term: %q", buffer)
			}
		}
	}
}

// FuzzCompile is the classifier fuzz target. Oracle is panic-only
// over both halves of the contract: any buffer under any grammar
// yields a usable value, and every read of that value answers.
func FuzzCompile(f *testing.F) {
	seeds := []string{
		"", "   ", "~", `\`, "cpu", "severity=critical", "a=1,b=2",
		"count>=5 && !severity=info || age<2h", "!(severity=info || team=ops)",
		"(", "()", "a=1)", "!", "web.*api", "/high.*infra/", `\(prod)`,
		"severity=~[", "^web(", `a=~"("`, "age<2x",
	}
	for _, s := range seeds {
		for _, g := range []uint8{0, 1, 2, 3} {
			f.Add(s, g)
		}
	}

	f.Fuzz(func(t *testing.T, buffer string, bits uint8) {
		g := filterexpr.Grammar{Selectors: bits&1 != 0, Expressions: bits&2 != 0}
		c, _ := filterexpr.Compile(buffer, g)
		require.Equal(t, buffer, c.Buffer())
		_ = c.Match(probeRow())
		_ = c.MatchAll()
		_ = c.ModeLabel()
		if spans := c.Spans(); spans != nil {
			_ = spans("high cpu")
		}
	})
}
