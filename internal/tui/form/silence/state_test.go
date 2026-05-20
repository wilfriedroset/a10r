// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/matcher"
)

func TestFormatMatchersRoundTrip(t *testing.T) {
	t.Parallel()
	// Includes values that themselves contain an operator-like
	// substring (`a!=b`, `a=b`, `=~regex`) so the leftmost-position
	// parser is exercised — alerts in the wild can carry such
	// values in annotations, and a lossy round-trip would silently
	// rewrite the matcher when the user opens an `e` form.
	in := []backend.Matcher{
		{Name: "alertname", Value: "HighCPU", IsEqual: true},
		{Name: "severity", Value: "warning|critical", IsRegex: true, IsEqual: true},
		{Name: "team", Value: "platform"},
		{Name: "instance", Value: ".*-canary", IsRegex: true},
		{Name: "expr", Value: "a!=b", IsEqual: true},
		{Name: "expr2", Value: "a=b", IsEqual: true},
		{Name: "expr3", Value: "x=~y", IsEqual: false},
	}
	rendered := formatMatchers(in)
	parsed, err := matcher.Parse(rendered)
	require.NoError(t, err)
	require.Equal(t, in, parsed)
}

func TestMatchersFromLabels_DropsNameAndSorts(t *testing.T) {
	t.Parallel()
	got := MatchersFromLabels(map[string]string{
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
