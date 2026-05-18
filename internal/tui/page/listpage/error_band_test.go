// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// healthMap is a tiny constructor for table-driven fixtures that
// only care about per-tenant Detail; State/Failures/NextAt default
// to zero values, which is the right shape for the renderer's
// detail-only sites (the live-countdown surface arrives in a
// follow-up commit).
func healthMap(byTenant map[string]string) map[string]listpage.BackendHealth {
	if byTenant == nil {
		return nil
	}
	out := make(map[string]listpage.BackendHealth, len(byTenant))
	for tenant, detail := range byTenant {
		out[tenant] = listpage.BackendHealth{Detail: detail}
	}
	return out
}

func TestBase_ErrorBand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		scope   string
		details map[string]string
		want    string
	}{
		{
			name: "no errors returns empty",
			want: "",
		},
		{
			name:    "empty detail entries are ignored",
			scope:   "prod",
			details: map[string]string{"prod": ""},
			want:    "",
		},
		{
			name:    "single tenant scope renders detail verbatim",
			scope:   "prod",
			details: map[string]string{"prod": "connection refused"},
			want:    "connection refused",
		},
		{
			name:    "out-of-scope error is filtered",
			scope:   "prod",
			details: map[string]string{"staging": "401"},
			want:    "",
		},
		{
			name:    "all scope with single offender prefixes tenant",
			scope:   "all",
			details: map[string]string{"prod": "401 unauthorised"},
			want:    "prod: 401 unauthorised",
		},
		{
			name:    "comma scope with single offender prefixes tenant",
			scope:   "prod,staging",
			details: map[string]string{"prod": "down"},
			want:    "prod: down",
		},
		{
			name:    "all scope with multiple offenders collapses by count",
			scope:   "all",
			details: map[string]string{"alpha": "down", "beta": "401"},
			want:    "2 backends erroring; alpha: down",
		},
		{
			name:    "sort by tenant is alphabetical regardless of map order",
			scope:   "all",
			details: map[string]string{"zeta": "z", "alpha": "a"},
			want:    "2 backends erroring; alpha: a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{
				Scope:         tc.scope,
				BackendHealth: healthMap(tc.details),
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
		details     map[string]string
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
			details:     map[string]string{"prod": "down"},
			width:       80,
			wantContent: "! down",
		},
		{
			name:        "long message is truncated to width",
			scope:       "prod",
			details:     map[string]string{"prod": strings.Repeat("x", 200)},
			width:       20,
			wantContent: "! ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{
				Scope:         tc.scope,
				BackendHealth: healthMap(tc.details),
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
