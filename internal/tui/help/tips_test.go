// SPDX-License-Identifier: Apache-2.0

package help

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTips_NotEmptyAndCopied(t *testing.T) {
	t.Parallel()

	got := Tips()
	require.NotEmpty(t, got, "the curated tip catalogue must not be empty")

	// Every entry must carry both fields — a half-populated tip has
	// no UI value (the hint bar would render a stray glyph or a
	// keyless description).
	for i, tip := range got {
		require.NotEmptyf(t, tip.Key, "tip[%d].Key must be set", i)
		require.NotEmptyf(t, tip.Text, "tip[%d].Text must be set", i)
	}

	// Mutating the returned slice must not bleed back into the next
	// call — Tips() copies on the way out so callers can iterate or
	// shuffle without cross-call coupling.
	got[0] = Tip{Key: "X", Text: "mutated"}
	fresh := Tips()
	require.NotEqual(t, "X", fresh[0].Key,
		"Tips() must hand out a fresh copy on every call")
}

func TestTips_DoesNotReferenceUnshippedFeatures(t *testing.T) {
	t.Parallel()

	// Future-plan features (custom keybindings, action registry
	// surface, Mimir config editor) must not appear in tips —
	// catalogue today only references features the binary already
	// ships. Catching the exact phrase is good enough; the curated
	// catalogue is small, so a substring guard keeps the test
	// simple without enumerating every banned word.
	banned := []string{
		"custom keybindings",
		"config editor",
		"action registry",
	}
	for _, tip := range Tips() {
		for _, bad := range banned {
			require.NotContainsf(t, tip.Text, bad,
				"tip %q references unshipped feature %q", tip.Key, bad)
		}
	}
}
