// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// LoadStyles returns a private copy of the default skin: the App writes
// through its styles pointer, so a shared one races parallel renders.
//
// If the first load fails, every subsequent caller sees tb.Fatalf
// rather than a zero-value Styles — sync.Once would otherwise let
// later callers run with a nil cache.
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
	styles := *cachedStyles
	return &styles
}

var (
	stylesOnce   sync.Once
	cachedStyles *theme.Styles
	errStyles    error
)
