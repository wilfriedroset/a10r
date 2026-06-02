// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// TestWrap confirms the helper matches the open-coded
// `lipgloss.NewStyle().Width(w).Render(body)` the six list pages
// used for the populated branch — width-pad only, natural height.
func TestWrap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		w    int
		body string
	}{
		{name: "single line", w: 10, body: "hello"},
		{name: "multiline body retains line count", w: 12, body: "row1\nrow2\nrow3"},
		{name: "empty body", w: 8, body: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := lipgloss.NewStyle().Width(tc.w).Render(tc.body)
			require.Equal(t, want, listpage.Wrap(tc.w, tc.body))
		})
	}
}
