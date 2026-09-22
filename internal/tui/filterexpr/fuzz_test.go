// SPDX-License-Identifier: Apache-2.0

package filterexpr_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
)

// FuzzParse is the grammar fuzz target. Parse either compiles or
// errors, and a compiled expression evaluates against a populated
// row. Two oracles run over Compile beside that panic check: a
// refused buffer carries no expression, and the refusal reads on
// one line, because the `[expr: <reason>]` title tag has one. The
// one-line rule is Compile's, not Parse's: only Compile runs the
// error through filter.RegexErrText, which drops the pattern text a
// regexp syntax error quotes back.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"   ",
		"cpu",
		"severity=critical",
		"a=1,b=2",
		"a=1&&b=2",
		"severity=critical team=infra",
		"count>=5 && !severity=info || age<2h",
		"!(severity=info || team=ops)",
		"()",
		"(a=1",
		"a=1)",
		"!",
		"a=1 &&",
		"a=1 ||",
		"foo>3",
		"age<2x",
		"count>x",
		"count=~5",
		"age=~2h",
		"state!~active",
		`count="5"`,
		`age="2h"`,
		`state=""`,
		"!=info",
		"foo !bar",
		"a<b=c || x",
		`alertname="a || b"`,
		`alertname="a, b"`,
		`state="a b"`,
		`\(prod)`,
		"web.*api",
		"/high.*infra/",
		"/a b/",
		"/a,b/",
		`/a\/b/`,
		"/a || b/",
		"a=1,",
		"count > 3",
		`alertname="a\" (b"`,
		`"a\">b"`,
		"web.*[",
		"(",
		"&&a=1",
		"/[/",
		"severity=~[",
		"~hci",
		strings.Repeat("(", 32) + "a=1" + strings.Repeat(")", 32),
		strings.Repeat("(", 33) + "a=1" + strings.Repeat(")", 33),
		strings.Repeat("!", 40) + "a=1",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		c, cerr := filterexpr.Compile(in, filterexpr.AlertGrammar)
		if cerr != nil {
			require.False(t, c.IsExpr())
			require.NotContains(t, cerr.Error(), "\n")
		}

		e, err := filterexpr.Parse(in)
		if err != nil {
			require.Nil(t, e)
			return
		}
		require.NotNil(t, e)
		_ = e.Match(baseRow())
	})
}
