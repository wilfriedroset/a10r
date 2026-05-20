// SPDX-License-Identifier: Apache-2.0

package pagetest

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Styles returns the default theme skin, lazily parsing the
// embedded YAML once per test binary. The cached *theme.Styles is
// shared across every caller in the package: lipgloss.Style values
// inside it are immutable from the outside (Render returns new
// strings, the struct itself is never written to), so the shared
// pointer is safe for parallel tests.
//
// sync.Once over a lazy init guard with mutex: the loader is
// idempotent and the value never invalidates inside one binary, so
// once-and-done is the cheaper fit. The embedded-assets exemption
// in CLAUDE.md covers this single package-level variable — every
// other piece of state in pagetest is request-scoped.
func Styles(tb testing.TB) *theme.Styles {
	tb.Helper()
	stylesOnce.Do(func() {
		s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
		require.NoError(tb, err)
		cachedStyles = s
	})
	require.NotNil(tb, cachedStyles,
		"cached styles must be populated — sync.Once initialiser failed")
	return cachedStyles
}

var (
	stylesOnce   sync.Once
	cachedStyles *theme.Styles
)
