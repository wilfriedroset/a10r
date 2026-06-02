// SPDX-License-Identifier: Apache-2.0

package action

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterDangerous_DropsDangerous(t *testing.T) {
	t.Parallel()

	// Read-only mode (C4) hides Dangerous bindings. The filter is
	// the single seam every consumer composes — pages apply it to
	// their Bindings() output before handing the slice to the hint
	// strip and the help overlay.
	in := []Action{
		{Key: "?", Description: "help"},
		{Key: "s", Description: "silence alert", Dangerous: true},
		{Key: "Shift+T", Description: "sort time"},
		{Key: "x", Description: "expire silence", Dangerous: true},
	}

	out := FilterDangerous(in)
	require.Len(t, out, 2)
	require.Equal(t, "?", out[0].Key)
	require.Equal(t, "Shift+T", out[1].Key)
}

func TestFilterDangerous_KeepsAllWhenNoneDangerous(t *testing.T) {
	t.Parallel()

	in := []Action{
		{Key: "?", Description: "help"},
		{Key: "j", Description: "down"},
	}

	out := FilterDangerous(in)
	require.Len(t, out, 2)
	require.Equal(t, in, out)
}

func TestFilterDangerous_EmptyInputReturnsEmptyOutput(t *testing.T) {
	t.Parallel()

	require.Empty(t, FilterDangerous(nil))
	require.Empty(t, FilterDangerous([]Action{}))
}

func TestFilterDangerous_ReturnsFreshSlice(t *testing.T) {
	t.Parallel()

	// Mutating the result must not bleed back into the input — the
	// callers (pages, help overlay) treat the output as read-only
	// but a future change that holds onto both slices would notice.
	in := []Action{{Key: "?", Description: "help"}}

	out := FilterDangerous(in)
	out[0].Key = "mutated"

	require.Equal(t, "?", in[0].Key,
		"FilterDangerous must not share storage with its input")
}

func TestAction_ChipKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   Action
		want string
	}{
		{"unset DisplayKey falls back to Key", Action{Key: ":"}, ":"},
		{"non-empty DisplayKey wins over Key", Action{Key: ":", DisplayKey: ":cmd"}, ":cmd"},
		{"explicit empty DisplayKey still falls back", Action{Key: "?", DisplayKey: ""}, "?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.in.ChipKey())
		})
	}
}
