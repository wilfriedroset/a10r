// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// exprAlerts is a five-instance HighCPU group (one of them
// suppressed, two of them info) beside a two-instance DiskFull group,
// so a COUNT threshold, an OR over severities and the state cycle all
// have something to separate.
func exprAlerts() []backend.Alert {
	return []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive, "fp-c1", 30*time.Minute, map[string]string{"instance": "a"}),
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp-c2", 30*time.Minute, map[string]string{"instance": "b"}),
		mkAlert("HighCPU", "warning", backend.AlertStateSuppressed, "fp-c3", 30*time.Minute, map[string]string{"instance": "c"}),
		mkAlert("HighCPU", "info", backend.AlertStateActive, "fp-c4", 30*time.Minute, map[string]string{"instance": "d"}),
		mkAlert("HighCPU", "info", backend.AlertStateActive, "fp-c5", 30*time.Minute, map[string]string{"instance": "e"}),
		mkAlert("DiskFull", "warning", backend.AlertStateActive, "fp-d1", 30*time.Minute, map[string]string{"instance": "f"}),
		mkAlert("DiskFull", "info", backend.AlertStateActive, "fp-d2", 30*time.Minute, map[string]string{"instance": "g"}),
	}
}

func exprSeed(t *testing.T) *Page {
	t.Helper()
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: exprAlerts()})
	return p
}

func viewFingerprints(p *Page) map[string][]string {
	out := map[string][]string{}
	for _, g := range p.groups {
		out[g.alertName] = fingerprints(g.instances)
	}
	return out
}

func TestExpr_FiltersTheView(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		filter string
		state  string
		want   map[string][]string
	}{
		{
			name:   "count keeps only the big group",
			filter: "count>=5",
			want:   map[string][]string{"HighCPU": {"fp-c1", "fp-c2", "fp-c3", "fp-c4", "fp-c5"}},
		},
		{
			name:   "or keeps both severities",
			filter: "severity=critical || severity=warning",
			want: map[string][]string{
				"HighCPU":  {"fp-c1", "fp-c2", "fp-c3"},
				"DiskFull": {"fp-d1"},
			},
		},
		{
			name:   "not drops the info instances",
			filter: "!severity=info",
			want: map[string][]string{
				"HighCPU":  {"fp-c1", "fp-c2", "fp-c3"},
				"DiskFull": {"fp-d1"},
			},
		},
		{
			name:   "state cycle ands with the expression",
			filter: "!severity=info",
			state:  string(backend.AlertStateActive),
			want: map[string][]string{
				"HighCPU":  {"fp-c1", "fp-c2"},
				"DiskFull": {"fp-d1"},
			},
		},
		{
			name:   "count sees the post-state-filter group size",
			filter: "count>=5",
			state:  string(backend.AlertStateActive),
			want:   map[string][]string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := exprSeed(t)
			p.Filter = tc.filter
			p.stateFilter = tc.state
			p.recompute()
			require.Equal(t, tc.want, viewFingerprints(p))
		})
	}
}

func TestExpr_AgeReEvaluatesAgainstTheClock(t *testing.T) {
	t.Parallel()

	now := fixedNow
	p := New(Options{Styles: pagetest.Styles(t), Now: func() time.Time { return now }})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp-c1", 30*time.Minute, nil),
	}})

	p.Filter = "age>1h"
	p.recompute()
	require.Empty(t, p.groups, "a 30 min old group is younger than the threshold")

	now = fixedNow.Add(45 * time.Minute)
	p.recompute()
	require.Len(t, p.groups, 1, "the same data ages into the filter on the next tick")
}

func TestExpr_CountIsThePreExpressionGroupSize(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("WebDown", "critical", backend.AlertStateActive, "fp-w1", time.Minute, map[string]string{"instance": "a"}),
		mkAlert("WebDown", "warning", backend.AlertStateActive, "fp-w2", time.Minute, map[string]string{"instance": "b"}),
		mkAlert("WebDown", "warning", backend.AlertStateActive, "fp-w3", time.Minute, map[string]string{"instance": "c"}),
		mkAlert("WebDown", "info", backend.AlertStateActive, "fp-w4", time.Minute, map[string]string{"instance": "d"}),
		mkAlert("Quiet", "critical", backend.AlertStateActive, "fp-q1", time.Minute, nil),
	}})

	p.Filter = "count>=3 && severity=critical"
	p.recompute()

	require.Len(t, p.groups, 1, "only the four-instance group clears the threshold")
	require.Equal(t, "WebDown", p.groups[0].alertName)
	require.Equal(t, []string{"fp-w1"}, fingerprints(p.groups[0].instances),
		"the group is kept by its pre-expression size but shows only the matching instance")
	require.Equal(t, 1, p.groups[0].count)
}

func TestExpr_BuffersWithoutNewTokensKeepTheOldPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		filter string
		want   map[string][]string
	}{
		{
			name:   "fuzzy",
			filter: "~hghcpu",
			want:   map[string][]string{"HighCPU": {"fp-c1", "fp-c2", "fp-c3", "fp-c4", "fp-c5"}},
		},
		{
			name:   "label matcher",
			filter: "severity=critical",
			want:   map[string][]string{"HighCPU": {"fp-c1"}},
		},
		{
			name:   "substring",
			filter: "diskfull",
			want:   map[string][]string{"DiskFull": {"fp-d1", "fp-d2"}},
		},
		{
			name:   "two-meta regex",
			filter: "high.*cpu",
			want:   map[string][]string{"HighCPU": {"fp-c1", "fp-c2", "fp-c3", "fp-c4", "fp-c5"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := exprSeed(t)
			p.Filter = tc.filter
			p.recompute()
			require.Equal(t, tc.want, viewFingerprints(p))
		})
	}
}

// TestExpr_GroupAndInstanceTermsComposeInOnePass is the spec's
// headline buffer, and the case a two-pass split cannot satisfy:
// `count` and `age` are group values, `severity` is an instance
// value, and the two sides of the `||` must each decide on their own.
func TestExpr_GroupAndInstanceTermsComposeInOnePass(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive, "fp-c1", 3*time.Hour, nil),
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp-c2", 3*time.Hour, nil),
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp-c3", 3*time.Hour, nil),
		mkAlert("HighCPU", "info", backend.AlertStateActive, "fp-c4", 3*time.Hour, nil),
		mkAlert("HighCPU", "info", backend.AlertStateActive, "fp-c5", 3*time.Hour, nil),
		mkAlert("DiskFull", "info", backend.AlertStateActive, "fp-d1", 30*time.Minute, nil),
		mkAlert("DiskFull", "warning", backend.AlertStateActive, "fp-d2", 30*time.Minute, nil),
	}})

	p.Filter = "count>=5 && !severity=info || age<2h"
	p.recompute()

	require.Equal(t, map[string][]string{
		"HighCPU":  {"fp-c1", "fp-c2", "fp-c3"},
		"DiskFull": {"fp-d1", "fp-d2"},
	}, viewFingerprints(p),
		"the old group passes on its size minus its info rows, the young one passes whole")
}

// TestExpr_UnparsableBufferFallsBackToTheFiveModePath pins what the
// page does with a buffer the prompt validator will later refuse: it
// searches for the text rather than matching everything.
func TestExpr_UnparsableBufferFallsBackToTheFiveModePath(t *testing.T) {
	t.Parallel()

	p := exprSeed(t)
	p.Filter = "count>=5 ||"
	p.recompute()

	require.Empty(t, p.groups)
}
