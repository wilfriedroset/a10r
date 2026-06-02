// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// LoadStyles returns the default theme skin, lazily parsing the
// embedded YAML once per test binary. Safe to share across parallel
// tests: lipgloss.Style values inside the struct are immutable from
// the outside (Render returns new strings, the struct itself is
// never written to). If the first load fails, every subsequent
// caller sees tb.Fatalf rather than a zero-value Styles — sync.Once
// would otherwise let later callers run with a nil cache.
func LoadStyles(tb testing.TB) *theme.Styles {
	tb.Helper()
	stylesOnce.Do(func() {
		s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
		if err != nil {
			errStyles = err
			return
		}
		cachedStyles = s
	})
	if errStyles != nil {
		tb.Fatalf("LoadStyles: %v", errStyles)
	}
	require.NotNil(tb, cachedStyles,
		"cached styles must be populated — sync.Once initialiser failed")
	return cachedStyles
}

var (
	stylesOnce   sync.Once
	cachedStyles *theme.Styles
	errStyles    error
)
