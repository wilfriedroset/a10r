// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// TestPane confirms the helper's output is byte-identical to the
// open-coded `lipgloss.NewStyle().Width(w).Height(h).Render(body)`
// that the six list pages used before C3. Identity is the contract:
// pages call Pane to mean "pad to w×h with no styling", and any
// drift would surface as terminal output regressions.
func TestPane(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		w, h int
		body string
	}{
		{name: "single-char body", w: 5, h: 3, body: "x"},
		{name: "narrow body wider pane", w: 20, h: 4, body: "hi"},
		{name: "multiline body", w: 12, h: 6, body: "first\nsecond"},
		{name: "empty body", w: 8, h: 2, body: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := lipgloss.NewStyle().Width(tc.w).Height(tc.h).Render(tc.body)
			require.Equal(t, want, listpage.Pane(tc.w, tc.h, tc.body))
		})
	}
}
