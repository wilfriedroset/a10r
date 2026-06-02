// SPDX-License-Identifier: Apache-2.0

package pagetest

import (
	"testing"

	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Styles returns the default theme skin. Thin wrapper preserved so
// existing page tests don't need to import testutil; the cache
// lives in testutil so every fuzz, bench, and page test shares one
// copy.
func Styles(tb testing.TB) *theme.Styles {
	tb.Helper()
	return testutil.LoadStyles(tb)
}
