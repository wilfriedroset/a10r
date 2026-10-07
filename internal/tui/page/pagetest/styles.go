// SPDX-License-Identifier: Apache-2.0

package pagetest

import (
	"testing"

	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Styles returns a private copy of the default theme skin. Thin
// wrapper preserved so existing page tests don't need to import
// testutil.
func Styles(tb testing.TB) *theme.Styles {
	tb.Helper()
	return testutil.LoadStyles(tb)
}
