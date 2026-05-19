// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAlertsArgs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		args      []string
		wantState string
		wantFltr  string
		wantErr   string
	}{
		{
			name: "no args",
			args: nil,
		},
		{
			name:      "state space-separated",
			args:      []string{"--state", "suppressed"},
			wantState: "suppressed",
		},
		{
			name:      "state equals form",
			args:      []string{"--state=active"},
			wantState: "active",
		},
		{
			name:      "state uppercased input lowers",
			args:      []string{"--state", "Active"},
			wantState: "active",
		},
		{
			name:     "filter space-separated",
			args:     []string{"--filter", "web"},
			wantFltr: "web",
		},
		{
			name:      "filter then state",
			args:      []string{"--filter=api", "--state", "active"},
			wantState: "active",
			wantFltr:  "api",
		},
		{
			name:      "positional `list` dropped",
			args:      []string{"list", "--state", "suppressed"},
			wantState: "suppressed",
		},
		{
			name:      "two positionals before flag",
			args:      []string{"list", "verbose", "--state=suppressed"},
			wantState: "suppressed",
		},
		{
			name:    "unknown flag rejected",
			args:    []string{"--severity", "critical"},
			wantErr: "unknown flag --severity",
		},
		{
			name:    "invalid state value rejected",
			args:    []string{"--state", "foobar"},
			wantErr: "--state \"foobar\"",
		},
		{
			name:    "missing value for trailing flag",
			args:    []string{"--state"},
			wantErr: "--state: missing value",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseAlertsArgs(tc.args)
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantState, got.state)
			require.Equal(t, tc.wantFltr, got.filter)
		})
	}
}

func TestParseFlagToken(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		input    string
		wantKey  string
		wantVal  string
		wantHasE bool
	}{
		{name: "long flag bare", input: "--state", wantKey: "state"},
		{name: "long flag eq", input: "--state=active", wantKey: "state", wantVal: "active", wantHasE: true},
		{name: "positional", input: "list"},
		{name: "double dash only", input: "--"},
		{name: "empty body after dashes", input: "--="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotK, gotV, gotE := parseFlagToken(tc.input)
			require.Equal(t, tc.wantKey, gotK)
			require.Equal(t, tc.wantVal, gotV)
			require.Equal(t, tc.wantHasE, gotE)
		})
	}
}
