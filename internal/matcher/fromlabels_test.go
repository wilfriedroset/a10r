// SPDX-License-Identifier: Apache-2.0

package matcher

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
)

func TestFromLabels_DropsNameAndSorts(t *testing.T) {
	t.Parallel()
	got := FromLabels(map[string]string{
		"__name__":  "ALERTS",
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "host-1",
	})
	require.Equal(t, []backend.Matcher{
		{Name: "alertname", Value: "HighCPU", IsEqual: true},
		{Name: "instance", Value: "host-1", IsEqual: true},
		{Name: "severity", Value: "critical", IsEqual: true},
	}, got, "synthetic __name__ must be dropped; output stable-sorted by name")
}

func TestFromLabels_Empty(t *testing.T) {
	t.Parallel()
	require.Empty(t, FromLabels(nil))
	require.Empty(t, FromLabels(map[string]string{"__name__": "ALERTS"}))
}
