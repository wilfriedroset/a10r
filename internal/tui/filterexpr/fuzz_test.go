// SPDX-License-Identifier: Apache-2.0

package filterexpr_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
)

// FuzzParse is the grammar fuzz target. Oracle is panic-only on
// both halves of the contract: Parse either compiles or errors,
// and a compiled expression evaluates against a populated row.
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
		// Compile wraps Parse, so the extra surface it fuzzes is the
		// isExpr scan that decides whether Parse runs at all.
		_, _ = filterexpr.Compile(in)

		e, err := filterexpr.Parse(in)
		if err != nil {
			require.Nil(t, e)
			return
		}
		require.NotNil(t, e)
		_ = e.Match(baseRow())
	})
}
