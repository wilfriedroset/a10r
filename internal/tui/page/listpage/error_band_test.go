// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestBase_ErrorBand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		scope      string
		lastErrors map[string]string
		want       string
	}{
		{
			name: "no errors returns empty",
			want: "",
		},
		{
			name:       "empty detail entries are ignored",
			scope:      "prod",
			lastErrors: map[string]string{"prod": ""},
			want:       "",
		},
		{
			name:       "single tenant scope renders detail verbatim",
			scope:      "prod",
			lastErrors: map[string]string{"prod": "connection refused"},
			want:       "connection refused",
		},
		{
			name:       "out-of-scope error is filtered",
			scope:      "prod",
			lastErrors: map[string]string{"staging": "401"},
			want:       "",
		},
		{
			name:       "all scope with single offender prefixes tenant",
			scope:      "all",
			lastErrors: map[string]string{"prod": "401 unauthorised"},
			want:       "prod: 401 unauthorised",
		},
		{
			name:       "comma scope with single offender prefixes tenant",
			scope:      "prod,staging",
			lastErrors: map[string]string{"prod": "down"},
			want:       "prod: down",
		},
		{
			name:       "all scope with multiple offenders collapses by count",
			scope:      "all",
			lastErrors: map[string]string{"alpha": "down", "beta": "401"},
			want:       "2 backends erroring; alpha: down",
		},
		{
			name:       "sort by tenant is alphabetical regardless of map order",
			scope:      "all",
			lastErrors: map[string]string{"zeta": "z", "alpha": "a"},
			want:       "2 backends erroring; alpha: a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{
				Scope:      tc.scope,
				LastErrors: tc.lastErrors,
			}
			require.Equal(t, tc.want, b.ErrorBand())
		})
	}
}

func TestBase_RenderErrorBand(t *testing.T) {
	t.Parallel()

	red := lipgloss.Color("#ff0000")

	cases := []struct {
		name        string
		scope       string
		lastErrors  map[string]string
		width       int
		wantEmpty   bool
		wantContent string
	}{
		{
			name:      "no errors renders empty",
			wantEmpty: true,
		},
		{
			name:        "short message fits",
			scope:       "prod",
			lastErrors:  map[string]string{"prod": "down"},
			width:       80,
			wantContent: "! down",
		},
		{
			name:        "long message is truncated to width",
			scope:       "prod",
			lastErrors:  map[string]string{"prod": strings.Repeat("x", 200)},
			width:       20,
			wantContent: "! ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{
				Scope:      tc.scope,
				LastErrors: tc.lastErrors,
			}
			out := b.RenderErrorBand(tc.width, red)
			if tc.wantEmpty {
				require.Empty(t, out)
				return
			}
			require.LessOrEqual(t, lipgloss.Width(out), tc.width,
				"render must fit within width")
			require.Contains(t, out, tc.wantContent,
				"render must include the expected prefix or content")
		})
	}
}
