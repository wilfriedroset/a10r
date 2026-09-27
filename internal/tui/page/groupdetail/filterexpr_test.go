// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// exprInstance builds an instance with an explicit start time so the
// age terms have something deterministic to compare against.
func exprInstance(fp, sev string, state backend.AlertState, startsAt time.Time) backend.Alert {
	a := instance(fp, sev, state, map[string]string{sortKeyInstance: fp})
	a.StartsAt = startsAt
	return a
}

// exprPage seeds one young critical, one young suppressed, one old
// warning, and one instance with no StartsAt at all.
func exprPage(t *testing.T) *Page {
	t.Helper()
	return newPage(t,
		exprInstance("fp-1", "critical", backend.AlertStateActive, fixedNow.Add(-30*time.Minute)),
		exprInstance("fp-2", "warning", backend.AlertStateSuppressed, fixedNow.Add(-30*time.Minute)),
		exprInstance("fp-3", "warning", backend.AlertStateActive, fixedNow.Add(-3*time.Hour)),
		exprInstance("fp-4", "info", backend.AlertStateActive, time.Time{}),
	)
}

func viewFingerprints(p *Page) []string {
	out := make([]string, 0, len(p.view))
	for _, e := range p.view {
		out = append(out, e.a.Fingerprint)
	}
	return out
}

func TestExpr_FiltersTheView(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		filter string
		state  string
		want   []string
	}{
		{
			name:   "state term",
			filter: "state=suppressed",
			want:   []string{"fp-2"},
		},
		{
			name:   "state term ands with the state cycle",
			filter: "state=suppressed",
			state:  string(backend.AlertStateActive),
			want:   []string{},
		},
		{
			name:   "state term agrees with the state cycle",
			filter: "state=suppressed",
			state:  string(backend.AlertStateSuppressed),
			want:   []string{"fp-2"},
		},
		{
			name:   "count is undefined here",
			filter: "count>=1",
			want:   []string{},
		},
		{
			name:   "negating an undefined count matches nothing either",
			filter: "!count>=1",
			want:   []string{},
		},
		{
			name:   "age compares each instance own start",
			filter: "age<2h",
			want:   []string{"fp-1", "fp-2"},
		},
		{
			name:   "a zero start fails the negated age term too",
			filter: "!age<2h",
			want:   []string{"fp-3"},
		},
		{
			name:   "or over severities",
			filter: "severity=critical || severity=info",
			want:   []string{"fp-1", "fp-4"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := exprPage(t)
			require.True(t, p.SetFilter(tc.filter))
			p.stateFilter = tc.state
			p.recompute()
			require.ElementsMatch(t, tc.want, viewFingerprints(p))
		})
	}
}

func TestExpr_AgeReEvaluatesAgainstTheClock(t *testing.T) {
	t.Parallel()

	now := fixedNow
	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return now },
		Tenant:    tenant,
		AlertName: alertName,
		Instances: []backend.Alert{
			exprInstance("fp-1", "critical", backend.AlertStateActive, fixedNow.Add(-30*time.Minute)),
		},
		Session: testutil.Session(),
	})

	require.True(t, p.SetFilter("age<1h"))
	p.recompute()
	require.Equal(t, []string{"fp-1"}, viewFingerprints(p))

	now = fixedNow.Add(45 * time.Minute)
	p.recompute()
	require.Empty(t, viewFingerprints(p), "the same instance ages out of the filter on the next tick")
}

func TestExpr_BuffersWithoutNewTokensKeepTheOldPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		filter string
		want   []string
	}{
		{name: "fuzzy", filter: "~fp1", want: []string{"fp-1"}},
		{name: "label matcher", filter: "severity=warning", want: []string{"fp-2", "fp-3"}},
		{name: "substring", filter: "fp-4", want: []string{"fp-4"}},
		{name: "two-meta regex", filter: "fp.*3", want: []string{"fp-3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := exprPage(t)
			require.True(t, p.SetFilter(tc.filter))
			p.recompute()
			require.ElementsMatch(t, tc.want, viewFingerprints(p))
		})
	}
}

// TestExpr_UnparsableBufferNeverBecomesTheFilter pins the refusal at
// the page: an expression the parser owns but cannot read leaves the
// rows on the last good buffer and reports why.
func TestExpr_UnparsableBufferNeverBecomesTheFilter(t *testing.T) {
	t.Parallel()

	p := exprPage(t)
	require.True(t, p.SetFilter("severity=warning"))
	p.recompute()
	kept := viewFingerprints(p)
	require.NotEmpty(t, kept)

	require.False(t, p.SetFilter("state=active ||"))
	p.recompute()
	require.Equal(t, kept, viewFingerprints(p), "a refused buffer leaves the rows alone")
	require.EqualError(t, p.FilterErr, "expr: missing term after ||")
}
