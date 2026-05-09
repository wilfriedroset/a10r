// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

func TestSeverity_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "ok", SeverityOK.String())
	require.Equal(t, "warning", SeverityWarning.String())
	require.Equal(t, "error", SeverityError.String())
	// Zero value of the string-typed enum is "" — render as
	// "unknown" so a misuse is visible rather than blank.
	require.Equal(t, "unknown", Severity("").String())
}

// recordingChecker captures the order and arguments of Run calls
// so tests can assert orchestration without invoking real probes.
type recordingChecker struct {
	name string
	hits *[]string
	want Severity
}

func (r recordingChecker) Name() string { return r.name }
func (r recordingChecker) Run(_ context.Context, b config.Backend, _ backend.Client) Result {
	*r.hits = append(*r.hits, r.name+":"+b.Name)
	return Result{Backend: b.Name, Check: r.name, Severity: r.want}
}

func TestRun_OrchestratesCheckersAcrossBackends(t *testing.T) {
	t.Parallel()

	var hits []string
	cs := []Checker{
		recordingChecker{name: "first", hits: &hits, want: SeverityOK},
		recordingChecker{name: "second", hits: &hits, want: SeverityWarning},
	}
	backends := []config.Backend{{Name: "prod"}, {Name: "staging"}}

	results := Run(t.Context(), backends, map[string]backend.Client{}, cs)

	require.Len(t, results, 4)
	// Backend-major, checker-minor order pinned: prod runs every
	// check before staging starts so multi-backend output reads
	// top-to-bottom per backend.
	require.Equal(t, []string{
		"first:prod", "second:prod",
		"first:staging", "second:staging",
	}, hits)
	require.Equal(t, SeverityOK, results[0].Severity)
	require.Equal(t, SeverityWarning, results[1].Severity)
}

func TestRun_HonoursContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // pre-cancelled — no checker should run

	var hits []string
	cs := []Checker{recordingChecker{name: "first", hits: &hits}}
	backends := []config.Backend{{Name: "prod"}}

	results := Run(ctx, backends, map[string]backend.Client{}, cs)
	require.Empty(t, results)
	require.Empty(t, hits, "cancelled context aborts before any checker runs")
}
