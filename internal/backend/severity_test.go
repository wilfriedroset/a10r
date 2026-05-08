// SPDX-License-Identifier: Apache-2.0

package backend

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeverityRank(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		labels map[string]string
		want   int
	}{
		{name: "critical", labels: map[string]string{"severity": "critical"}, want: 3},
		{name: "warning", labels: map[string]string{"severity": "warning"}, want: 2},
		{name: "info", labels: map[string]string{"severity": "info"}, want: 1},
		{name: "case insensitive", labels: map[string]string{"severity": "Critical"}, want: 3},
		{name: "unknown drops to zero", labels: map[string]string{"severity": "fatal"}, want: 0},
		{name: "missing label is zero", labels: nil, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SeverityRank(tc.labels)
			require.Equal(t, tc.want, got)
		})
	}
}
