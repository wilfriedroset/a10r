// SPDX-License-Identifier: Apache-2.0

package filterexpr_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
)

var testNow = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func baseRow() filterexpr.Row {
	return filterexpr.Row{
		Now:        testNow,
		Labels:     map[string]string{"severity": "critical", "team": "infra"},
		Text:       "highcpu critical infra",
		State:      "active",
		Instance:   filterexpr.Present,
		Count:      3,
		CountAvail: filterexpr.Present,
		Start:      testNow.Add(-time.Hour),
		AgeAvail:   filterexpr.Present,
	}
}

func mustParse(t *testing.T, in string) *filterexpr.Expr {
	t.Helper()
	e, err := filterexpr.Parse(in)
	require.NoError(t, err)
	require.NotNil(t, e)
	return e
}

// TestPrecedence pins `&&` tighter than `||` and `!` tighter than
// both, by evaluation rather than by shape: the first row is the
// discriminator between the two readings.
func TestPrecedence(t *testing.T) {
	t.Parallel()

	e := mustParse(t, "count>=5 && !severity=info || age<2h")

	mk := func(count int, severity string, age time.Duration) filterexpr.Row {
		r := baseRow()
		r.Count = count
		r.Labels = map[string]string{"severity": severity}
		r.Start = testNow.Add(-age)
		return r
	}

	tests := []struct {
		name string
		row  filterexpr.Row
		want bool
	}{
		// (false && …) || true. The count>=5 && (… || age<2h)
		// misreading yields false here.
		{"or arm alone carries the row", mk(1, "info", time.Hour), true},
		{"and arm alone carries the row", mk(9, "warn", 5*time.Hour), true},
		{"negated label kills the and arm", mk(9, "info", 5*time.Hour), false},
		{"neither arm", mk(1, "warn", 5*time.Hour), false},
		{"both arms", mk(9, "warn", time.Hour), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, e.Match(tc.row))
		})
	}
}

// TestAndSpellings covers juxtaposition, `,` and `&&` meaning the
// same conjunction.
func TestAndSpellings(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"severity=critical team=infra",
		"severity=critical,team=infra",
		"severity=critical&&team=infra",
		"severity=critical , team=infra",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			e := mustParse(t, in)
			require.True(t, e.Match(baseRow()))

			partial := baseRow()
			partial.Labels = map[string]string{"severity": "critical"}
			require.False(t, e.Match(partial))
		})
	}
}

func TestParenthesesOverridePrecedence(t *testing.T) {
	t.Parallel()

	row := baseRow()
	row.Labels = map[string]string{"severity": "info", "team": "ops"}

	grouped := mustParse(t, "(severity=info || severity=critical) && team=infra")
	require.False(t, grouped.Match(row))

	flat := mustParse(t, "severity=info || severity=critical && team=infra")
	require.True(t, flat.Match(row))
}

func TestNotAppliesToGroup(t *testing.T) {
	t.Parallel()

	e := mustParse(t, "!(severity=info || team=ops)")
	require.True(t, e.Match(baseRow()))

	hit := baseRow()
	hit.Labels = map[string]string{"severity": "info"}
	require.False(t, e.Match(hit))
}

func TestNestingCap(t *testing.T) {
	t.Parallel()

	const cap32 = 32
	ok := strings.Repeat("(", cap32) + "severity=critical" + strings.Repeat(")", cap32)
	e := mustParse(t, ok)
	require.True(t, e.Match(baseRow()))

	tooDeep := strings.Repeat("(", cap32+1) + "severity=critical" + strings.Repeat(")", cap32+1)
	_, err := filterexpr.Parse(tooDeep)
	require.EqualError(t, err, "nesting deeper than 32")

	// `!` shares the counter with `(`: it is the only bound on
	// parseNot's recursion, so a pathological run of them must stop
	// at the same depth rather than at a stack overflow.
	bangs := mustParse(t, strings.Repeat("!", cap32)+"severity=critical")
	require.True(t, bangs.Match(baseRow()))

	_, err = filterexpr.Parse(strings.Repeat("!", cap32+1) + "severity=critical")
	require.EqualError(t, err, "nesting deeper than 32")
}

// TestDelimitersEndATerm pins the lexer's delimiter rule and the
// cost that comes with it: a `(` in a label value ends the term, so
// a selector that needs one has to be quoted.
func TestDelimitersEndATerm(t *testing.T) {
	t.Parallel()

	row := baseRow()
	row.Labels = map[string]string{"severity": "critical"}
	row.Text = "a(b)c highcpu"

	require.True(t, mustParse(t, `severity=~"(crit|warn).*"`).Match(row))
	require.False(t, mustParse(t, "severity=~(crit|warn).*").Match(row))
	require.False(t, mustParse(t, "count > 3").Match(baseRow()))
}

// TestSlashRegexTerm pins the `/.../` escape from the delimiter
// rule: between the slashes every byte is ordinary, so a pattern can
// carry a group, a space or a `,` that would otherwise end the term.
func TestSlashRegexTerm(t *testing.T) {
	t.Parallel()

	row := baseRow()
	row.Text = `abc a b a,b a/ b a"b`

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "group", in: "/(a|b)c/", want: true},
		{name: "space", in: "/a b/", want: true},
		{name: "comma", in: "/a,b/", want: true},
		{name: "escaped slash", in: `/a\/b/`, want: false},
		{name: "escaped slash then space", in: `/a\/ b/`, want: true},
		{name: "anchored miss", in: "/^zzz/", want: false},
		{name: "quote is a regex byte", in: `/a"b/`, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, mustParse(t, tc.in).Match(row))
		})
	}
}

// TestSlashRegexStillEndsTerm proves the closing `/` hands control
// back to the delimiter scan, so a `/.../` term still composes.
func TestSlashRegexStillEndsTerm(t *testing.T) {
	t.Parallel()

	row := baseRow()
	row.Text = "abc highcpu"

	require.True(t, mustParse(t, "/(a|b)c/ && highcpu").Match(row))
	require.False(t, mustParse(t, "/(a|b)c/ && nosuch").Match(row))
	require.True(t, mustParse(t, "/(a|b)c/ || nosuch").Match(row))
}

// TestUnterminatedSlashTerm keeps the pre-escape behaviour for a
// `/` that never closes: the ordinary delimiter scan runs, so the
// buffer splits on whitespace and an unbalanced `(` still fails.
func TestUnterminatedSlashTerm(t *testing.T) {
	t.Parallel()

	row := baseRow()
	row.Text = "/a highcpu"

	require.True(t, mustParse(t, "/a || nosuch").Match(row))

	_, err := filterexpr.Parse("/a(b || highcpu")
	require.EqualError(t, err, "unbalanced (")
}

// TestCommaBindsTighterThanOr pins `,` as an AND spelling against
// `||`, not merely against `&&`.
func TestCommaBindsTighterThanOr(t *testing.T) {
	t.Parallel()

	e := mustParse(t, "severity=critical,team=infra || state=suppressed")
	require.True(t, e.Match(baseRow()))

	partial := baseRow()
	partial.Labels = map[string]string{"severity": "critical"}
	require.False(t, e.Match(partial))

	partial.State = "suppressed"
	require.True(t, e.Match(partial))
}

// TestQuotedValues proves the lexer treats operators, a space and a
// `,` inside double quotes as ordinary term bytes.
func TestQuotedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    string
		value string
	}{
		{`alertname="a || b"`, "a || b"},
		{`alertname="a && b"`, "a && b"},
		{`alertname="a, b"`, "a, b"},
		{`alertname="a b"`, "a b"},
		{`alertname="a(b)"`, "a(b)"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			e := mustParse(t, tc.in)

			hit := baseRow()
			hit.Labels = map[string]string{"alertname": tc.value}
			require.True(t, e.Match(hit))

			miss := baseRow()
			miss.Labels = map[string]string{"alertname": "a"}
			require.False(t, e.Match(miss))
		})
	}
}

// TestQuotedOperatorIsNotAComparison pins the quote tracking in the
// operator scan. A `>` between quotes is a searched byte, so the
// term stays a text search instead of failing as an order
// comparison against an unknown key. The quotes pick the mode and
// are not themselves searched, so the phrase alone is the needle.
func TestQuotedOperatorIsNotAComparison(t *testing.T) {
	t.Parallel()

	row := baseRow()
	row.Text = `x a>b y`

	require.True(t, mustParse(t, `"a>b"`).Match(row))
	require.True(t, mustParse(t, `"a>b" || nosuch`).Match(row))

	miss := baseRow()
	miss.Text = `x a b y`
	require.False(t, mustParse(t, `"a>b"`).Match(miss))
}

// TestEscapedQuoteStaysInsideTheValue pins that `\"` does not close
// a quoted value. Without the escape the quote closes early, the
// following `(` reads as grammar, and the buffer fails to parse.
// The backslash survives into the compared value: matcher.ParseOne
// strips the outer quotes and never unescapes, so the term matches
// a label spelled with the backslash, not one spelled without it.
// A quoted text term reads the same way -- the outer quotes go and
// nothing else does.
func TestEscapedQuoteStaysInsideTheValue(t *testing.T) {
	t.Parallel()

	kept := baseRow()
	kept.Labels = map[string]string{"alertname": `a\" (b`}
	require.True(t, mustParse(t, `alertname="a\" (b"`).Match(kept))
	require.False(t, mustParse(t, `alertname="a\" (b"`).Match(baseRow()))

	row := baseRow()
	row.Text = `x a\">b y`
	require.True(t, mustParse(t, `"a\">b"`).Match(row))

	unescaped := baseRow()
	unescaped.Text = `x a">b y`
	require.False(t, mustParse(t, `"a\">b"`).Match(unescaped))
}

func TestQuotedStateValue(t *testing.T) {
	t.Parallel()

	e := mustParse(t, `state="a b"`)
	row := baseRow()
	row.State = "a b"
	require.True(t, e.Match(row))
	require.False(t, e.Match(baseRow()))
}

// TestQuotedTypedValues pins one quoting rule for the typed keys:
// the outer pair of double quotes around the value is stripped
// before the value is read, and an unbalanced quote stays part of
// the value so the error names what the user typed.
func TestQuotedTypedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    bool
		wantErr string
	}{
		{in: `count="3"`, want: true},
		{in: `count="5"`, want: false},
		{in: `age="1h"`, want: true},
		{in: `age="2h"`, want: false},
		{in: `state="active"`, want: true},
		{in: `count=""`, wantErr: `bad count ""`},
		{in: `count="5`, wantErr: `bad count "\"5"`},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			e, err := filterexpr.Parse(tc.in)
			if tc.wantErr != "" {
				require.Nil(t, e)
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, e.Match(baseRow()))
		})
	}
}

// TestMissingIsNeitherTrueNorFalse pins the Kleene rule: a term over
// a value the row does not carry fails, and so does its negation.
func TestMissingIsNeitherTrueNorFalse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		row  filterexpr.Row
	}{
		{"count", "count>=1", func() filterexpr.Row {
			r := baseRow()
			r.CountAvail = filterexpr.Missing
			return r
		}()},
		{"label", "severity=critical", func() filterexpr.Row {
			r := baseRow()
			r.Instance = filterexpr.Missing
			return r
		}()},
		{"text", "critical", func() filterexpr.Row {
			r := baseRow()
			r.Instance = filterexpr.Missing
			return r
		}()},
		{"state", "state=active", func() filterexpr.Row {
			r := baseRow()
			r.Instance = filterexpr.Missing
			return r
		}()},
		{"age", "age<2h", func() filterexpr.Row {
			r := baseRow()
			r.AgeAvail = filterexpr.Missing
			return r
		}()},
		{"zero start", "age<2h", func() filterexpr.Row {
			r := baseRow()
			r.Start = time.Time{}
			return r
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.False(t, mustParse(t, tc.expr).Match(tc.row))
			require.False(t, mustParse(t, "!"+tc.expr).Match(tc.row))
		})
	}
}

// TestUnknownSurvivesACompositeStructure pins the unknown arm of
// all three combinators. A missing value has to stay unknown
// through an AND whose other kid is true, an OR whose other kid is
// false, and a `!`. Without the AND arm `count>=1 && severity=critical`
// would match every critical alert on a group-detail row. Without
// the OR arm `!(count>=1 || severity=nope)` would collapse to a
// match. Without the `!` arm a second negation would promote
// unknown back to a match.
func TestUnknownSurvivesACompositeStructure(t *testing.T) {
	t.Parallel()

	row := baseRow()
	row.CountAvail = filterexpr.Missing

	require.False(t, mustParse(t, "count>=1 && severity=critical").Match(row))
	require.False(t, mustParse(t, "severity=critical && count>=1").Match(row))
	require.False(t, mustParse(t, "!(count>=1 || severity=nope)").Match(row))
	require.False(t, mustParse(t, "!(severity=nope || count>=1)").Match(row))
	require.False(t, mustParse(t, "!!count>=1").Match(row))
	require.False(t, mustParse(t, "!(severity=nope || !count>=1)").Match(row))
}

func TestTypedTerms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want bool
	}{
		{"count=3", true},
		{"count!=3", false},
		{"count>2", true},
		{"count>3", false},
		{"count>=3", true},
		{"count<4", true},
		{"count<=2", false},
		{"age<2h", true},
		{"age>2h", false},
		{"age>=1h", true},
		{"age<1h30m", true},
		{"state=active", true},
		{"state!=active", false},
		{`state="active"`, true},
		{"state=ACTIVE", true},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, mustParse(t, tc.expr).Match(baseRow()))
		})
	}
}

func TestTextTerms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want bool
	}{
		{"cpu", true},
		{"CPU", true},
		{"nomatch", false},
		{"/high.*infra/", true},
		{"/^infra/", false},
		{"~hci", true},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, mustParse(t, tc.expr).Match(baseRow()))
		})
	}
}

func TestLabelRegexTerms(t *testing.T) {
	t.Parallel()

	require.True(t, mustParse(t, "severity=~crit.*").Match(baseRow()))
	require.False(t, mustParse(t, "severity!~crit.*").Match(baseRow()))
}

// TestUncompilableRegexReachesTheUser pins that a broken `=~` value
// is reported rather than silently demoted to a text search.
func TestUncompilableRegexReachesTheUser(t *testing.T) {
	t.Parallel()

	_, err := filterexpr.Parse("severity=~[")
	require.ErrorContains(t, err, "compile regex")

	_, err = filterexpr.Parse("/[/")
	require.ErrorContains(t, err, "error parsing regexp")

	e, err := filterexpr.Parse("web.*[")
	require.Nil(t, e)
	require.ErrorContains(t, err, "error parsing regexp")
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in  string
		err string
	}{
		{"", "empty filter"},
		{"   ", "empty filter"},
		{"(a=1", "unbalanced ("},
		{"(a=1 || b=2", "unbalanced ("},
		{"a=1)", "unexpected )"},
		{")", "unexpected )"},
		{"!", "empty term after !"},
		{"a=1 && !", "empty term after !"},
		{"foo>3", `unknown key "foo"`},
		{"FOO<=3", `unknown key "FOO"`},
		{"age<2x", `bad duration "2x"`},
		{"count>x", `bad count "x"`},
		{"count=~5", `bad operator "=~" for key "count"`},
		{"age!~2h", `bad operator "!~" for key "age"`},
		{"state=~act", `bad operator "=~" for key "state"`},
		{"a=1 &&", "missing term after &&"},
		{"a=1,", "missing term after &&"},
		{"a=1 ||", "missing term after ||"},
		{"()", "empty group"},
		{"a=1 && ()", "empty group"},
		{"(", "unbalanced ("},
		{"!(", "unbalanced ("},
		{"&&a=1", "missing term after &&"},
		{"||a=1", "missing term after ||"},
		{"a=1 || foo>3", `unknown key "foo"`},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			e, err := filterexpr.Parse(tc.in)
			require.Nil(t, e)
			require.EqualError(t, err, tc.err)
		})
	}
}

// TestTypedKeysAreReservedUnderEveryOperator pins that `count`,
// `age` and `state` are typed keys under every operator, so a regex
// operator on one of them reports the same error whether or not an
// unrelated term sits beside it in the buffer. The buffer never
// falls back to a label matcher on a label of that name.
func TestTypedKeysAreReservedUnderEveryOperator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		term string
		err  string
	}{
		{term: "age=~2h", err: `bad operator "=~" for key "age"`},
		{term: "count=~5", err: `bad operator "=~" for key "count"`},
		{term: "state!~active", err: `bad operator "!~" for key "state"`},
		{term: "state=~act.*", err: `bad operator "=~" for key "state"`},
		{term: "AGE=~2h", err: `bad operator "=~" for key "age"`},
	}
	for _, tc := range tests {
		t.Run(tc.term, func(t *testing.T) {
			t.Parallel()

			for _, buffer := range []string{tc.term, "a=1 || " + tc.term, "a=1," + tc.term} {
				c, err := filterexpr.Compile(buffer, filterexpr.AlertGrammar)
				require.EqualError(t, err, "expr: "+tc.err, "buffer %q", buffer)
				require.False(t, c.IsExpr(), "a refused buffer carries no expression: %q", buffer)
			}
		})
	}
}

// TestCompile_ExpressionGate walks the scan that decides whether the
// expression parser owns a buffer at all. The grammar reads
// expressions and nothing else, so a buffer the parser declines can
// only land on the five-mode text path, which the compiled value
// reports as any label but "expr".
func TestCompile_ExpressionGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		wantNil bool
		err     string
	}{
		{name: "empty buffer", in: "", wantNil: true},
		{name: "whitespace only", in: "   ", wantNil: true},
		{name: "plain substring", in: "high cpu", wantNil: true},
		{name: "label matcher", in: "severity=critical", wantNil: true},
		{name: "and chain", in: "a=1,b=2", wantNil: true},
		{name: "and chain with &&", in: "a=1&&b=2", wantNil: true},
		{name: "quoted or inside a value", in: `alertname="a || b"`, wantNil: true},
		{name: "regex op on a typed key reports", in: "count=~3", err: `bad operator "=~" for key "count"`},
		{name: "regex metas", in: "web.*api", wantNil: true},
		{name: "literal sigil escapes", in: `\(a || b)`, wantNil: true},
		{name: "or", in: "a=1 || b=2"},
		{name: "not", in: "!severity=info"},
		{name: "typed count", in: "count>=5"},
		{name: "typed key is case-insensitive", in: "COUNT>=3"},
		{name: "and chain with a typed term", in: "a=1 && count>3"},
		{name: "typed age", in: "age<2h"},
		{name: "typed state", in: "state=active"},
		{name: "balanced group", in: "(a=1 || b=2) && c=3"},
		{name: "a lone paren is not an expression signal", in: "(foo", wantNil: true},
		{name: "regex alternation keeps the five-mode path", in: "(web|api)", wantNil: true},
		{name: "regex alternation with a suffix", in: "(web|api).*", wantNil: true},
		{name: "paren plus and is a group", in: "(a=1 && b=2) c=3"},
		{name: "nested groups", in: "(a=1 && (b=2 || c=3))"},
		{name: "juxtaposed group stays five-mode", in: "(a=1 b=2)", wantNil: true},
		{name: "paren inside a quoted value", in: `alertname="a(b" && x=1`, wantNil: true},
		{name: "paren plus typed term reports", in: "(count>=5", err: "unbalanced ("},
		{name: "paren plus and, unbalanced, reports", in: "(a=1 && b=2", err: "unbalanced ("},
		{name: "or with missing term reports", in: "a=1 ||", err: "missing term after ||"},
		{name: "bare not reports", in: "!", err: "empty term after !"},
		{name: "bad typed value reports", in: "age<2x", err: `bad duration "2x"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, err := filterexpr.Compile(tc.in, filterexpr.Grammar{Expressions: true})
			switch {
			case tc.err != "":
				require.EqualError(t, err, "expr: "+tc.err)
			case tc.wantNil:
				require.NoError(t, err)
				require.NotEqual(t, "expr", e.ModeLabel(), "the five-mode path owns this buffer")
				require.False(t, e.IsExpr(),
					"a buffer the parser does not own leaves the expression half of the value empty")
			default:
				require.NoError(t, err)
				require.Equal(t, "expr", e.ModeLabel())
			}
		})
	}
}

// TestQuotedTextIsALiteralPhrase pins the quoted text operand: the
// outer quotes pick the mode rather than joining the needle, so the
// phrase inside is searched whole, spaces included, with no sigil
// and no regex auto-detect reading it.
func TestQuotedTextIsALiteralPhrase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		text string
		want bool
	}{
		{"a phrase carrying a space", `"disk full"`, "a disk full b", true},
		{"the same phrase inside an expression", `(severity=critical && "disk full")`, "a disk full b", true},
		{"a phrase that is not there", `"disk full"`, "diskfull", false},
		{"metacharacters are searched as text", `"web.*api"`, "a web.*api b", true},
		{"metacharacters no longer compile as a regex", `"web.*api"`, `"webxapi"`, false},
		{"a leading tilde is not a fuzzy sigil", `"~foo"`, "a ~foo b", true},
		{"a leading tilde is not a fuzzy sigil (miss)", `"~foo"`, "f o o", false},
		{"a leading backslash is not a literal sigil", `"\x"`, `a \x b`, true},
		{"an operator inside quotes stays text", `"a=1 b"`, "x a=1 b y", true},
		{"a lone quote is still searched as text", `"`, `a " b`, true},
		{"an empty phrase constrains nothing", `""`, "anything", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := baseRow()
			row.Text = tc.text
			require.Equal(t, tc.want, mustParse(t, tc.expr).Match(row))
		})
	}
}

// TestQuotedPhraseSurvivesCompile walks the buffer keybindings.md
// publishes through the entry point the chrome calls, because a
// quoted phrase only reads as one when the expression path owns the
// buffer. A bare `&&` chain does not reach it.
func TestQuotedPhraseSurvivesCompile(t *testing.T) {
	t.Parallel()

	row := baseRow()
	row.Text = "a disk full b"

	c, err := filterexpr.Compile(`(severity=critical && "disk full")`, filterexpr.AlertGrammar)
	require.NoError(t, err)
	require.True(t, c.IsExpr())
	require.True(t, c.Match(row))
}
