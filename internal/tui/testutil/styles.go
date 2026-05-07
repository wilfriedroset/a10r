// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// LoadStyles loads the default theme skin and returns the resolved
// styles, failing the test on any loader error. Page tests use it
// to seed a Page with the production palette without each test
// file owning its own loader boilerplate. Most page-test assertions
// strip styles before comparing, so the exact skin doesn't matter
// — what matters is that a non-zero Styles is plumbed through.
func LoadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}
