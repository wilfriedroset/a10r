// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// frozenNow is the deterministic clock the renderer table tests
// pass to ErrorBand / RenderErrorBand so the "Next attempt"
// suffix is reproducible.
var frozenNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

// healthMap is a tiny constructor for table-driven fixtures that
// only care about per-tenant Detail. NextAt is zero so all
// fixtures share the same `retrying now` suffix unless the test
// overrides via the seam below.
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

// healthMapAt is the variant for fixtures that want a non-zero
// NextAt so the suffix renders as `retrying in <unit>`.
func healthMapAt(byTenant map[string]string, nextAt time.Time) map[string]listpage.BackendHealth {
	if byTenant == nil {
		return nil
	}
	out := make(map[string]listpage.BackendHealth, len(byTenant))
	for tenant, detail := range byTenant {
		out[tenant] = listpage.BackendHealth{Detail: detail, NextAt: nextAt}
	}
	return out
}

func TestBase_ErrorBand(t *testing.T) {
	t.Parallel()

	in5s := frozenNow.Add(5 * time.Second)
	in2m := frozenNow.Add(2 * time.Minute)

	cases := []struct {
		name   string
		scope  string
		health map[string]listpage.BackendHealth
		want   string
	}{
		{
			name: "no errors returns empty",
			want: "",
		},
		{
			name:   "empty detail entries are ignored",
			scope:  "prod",
			health: healthMap(map[string]string{"prod": ""}),
			want:   "",
		},
		{
			name:   "single tenant scope renders detail with past-due suffix",
			scope:  "prod",
			health: healthMap(map[string]string{"prod": "connection refused"}),
			want:   "connection refused — retrying now",
		},
		{
			name:   "single tenant scope with future NextAt renders seconds",
			scope:  "prod",
			health: healthMapAt(map[string]string{"prod": "connection refused"}, in5s),
			want:   "connection refused — retrying in 5s",
		},
		{
			name:   "single tenant scope with NextAt > 1m renders minutes",
			scope:  "prod",
			health: healthMapAt(map[string]string{"prod": "down"}, in2m),
			want:   "down — retrying in 2m",
		},
		{
			name:   "out-of-scope error is filtered",
			scope:  "prod",
			health: healthMap(map[string]string{"staging": "401"}),
			want:   "",
		},
		{
			name:   "all scope with single offender prefixes tenant",
			scope:  "all",
			health: healthMap(map[string]string{"prod": "401 unauthorised"}),
			want:   "prod: 401 unauthorised — retrying now",
		},
		{
			name:   "comma scope with single offender prefixes tenant",
			scope:  "prod,staging",
			health: healthMap(map[string]string{"prod": "down"}),
			want:   "prod: down — retrying now",
		},
		{
			name:   "all scope with multiple offenders collapses by count",
			scope:  "all",
			health: healthMap(map[string]string{"alpha": "down", "beta": "401"}),
			want:   "2 backends erroring; alpha: down — retrying now",
		},
		{
			// Locks the ADR-stated invariant: the suffix tracks the
			// alphabetically-first offender's NextAt, NOT whichever
			// entry the map iterator happens to surface first.
			name:  "sort by tenant is alphabetical and suffix tracks the first offender's NextAt",
			scope: "all",
			health: map[string]listpage.BackendHealth{
				"zeta":  {Detail: "z", NextAt: in2m},
				"alpha": {Detail: "a", NextAt: in5s},
			},
			want: "2 backends erroring; alpha: a — retrying in 5s",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{
				Scope:         tc.scope,
				BackendHealth: tc.health,
			}
			require.Equal(t, tc.want, b.ErrorBand(frozenNow))
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
			name:        "short message fits with retry suffix",
			scope:       "prod",
			details:     map[string]string{"prod": "down"},
			width:       80,
			wantContent: "! down — retrying now",
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
			out := b.RenderErrorBand(frozenNow, tc.width, red)
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
