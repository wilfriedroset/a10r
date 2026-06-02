// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func newWritablePage(t *testing.T, instances ...backend.Alert) *Page {
	t.Helper()
	return New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Clients:   map[string]silenceform.Client{tenant: &testutil.FakeSilenceClient{}},
		Instances: instances,
	})
}

func TestSilenceOne_NoMarksPushesForm(t *testing.T) {
	t.Parallel()
	p := newWritablePage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
	)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	if _, isFlash := cmd().(footer.FlashShowMsg); isFlash {
		t.Fatal("s with no marks on a writable page must push the silence form, not flash")
	}
}

func TestSilenceOne_NoWritableBackendFlashes(t *testing.T) {
	t.Parallel()
	p := newPage(t, // no Clients
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
	)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "no writeable backend")
}

func TestBulkSilence_TwoMarksOpensConfirmWithoutWarning(t *testing.T) {
	t.Parallel()
	q := bulkSilenceQuestion(2, tenant)
	require.Contains(t, q, "silence 2 instances?")
	require.NotContains(t, q, "silence-all",
		"below the warn threshold the confirm must not nudge toward silence-all")
}

func TestBulkSilence_TenMarksConfirmIncludesWarning(t *testing.T) {
	t.Parallel()
	q := bulkSilenceQuestion(10, tenant)
	require.Contains(t, q, "10 individual silences will be created")
	require.Contains(t, q, "Esc and use silence-all to silence the whole alert instead.")
}

func TestBulkSilence_MarkedFanoutResolvesByFingerprint(t *testing.T) {
	t.Parallel()
	insts := make([]backend.Alert, 3)
	for i := range insts {
		insts[i] = instance(fmt.Sprintf("fp-%d", i), "warning", backend.AlertStateActive,
			map[string]string{sortKeyInstance: fmt.Sprintf("web-%d", i)})
	}
	p := newWritablePage(t, insts...)
	// Mark all three.
	for range insts {
		_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Len(t, p.marks, 3)

	// s with marks resolves targets (sorted by fingerprint).
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	require.Len(t, p.pendingBulkSilence.targets, 3)
	require.Equal(t, "fp-0", p.pendingBulkSilence.targets[0].Fingerprint)
	require.Equal(t, "fp-2", p.pendingBulkSilence.targets[2].Fingerprint)
}

func TestBulkSilence_MarkOnFilteredOutInstanceStillFansOut(t *testing.T) {
	t.Parallel()
	p := newWritablePage(t,
		instance("fp-web", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		instance("fp-db", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: dbInst}),
	)
	// Mark both rows.
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Len(t, p.marks, 2)

	// Filter so db-1 is hidden; the marked-but-hidden instance must
	// still be in the resolved targets (walks instances, not view).
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: webFilter})
	require.Len(t, p.view, 1)

	targets := p.resolveBulkSilenceTargets()
	require.Len(t, targets, 2)
}

func TestBulkSilence_FanoutRoundTripDropsMarksAndFlashes(t *testing.T) {
	t.Parallel()
	p := newWritablePage(t,
		instance("fp-0", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst0}),
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
	)
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Len(t, p.marks, 2)

	// s opens the confirm (N=2); Yes pushes the bulk form.
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Len(t, p.pendingBulkSilence.targets, 2)
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})

	// The form's submit fans out one CreateSilence per marked instance.
	_, cmd := p.Update(silenceform.BulkSubmittedMsg{
		Creator:  "alice",
		StartsAt: fixedNow,
		EndsAt:   fixedNow.Add(time.Hour),
	})
	require.NotNil(t, cmd)
	done, ok := cmd().(bulkop.DoneMsg[string])
	require.True(t, ok, "submit must dispatch a bulkop fanout that resolves to DoneMsg")
	require.Len(t, done.Results, 2)
	for _, r := range done.Results {
		require.NoError(t, r.Err, "FakeSilenceClient creates succeed")
	}

	// Applying the result drops every succeeded mark and flashes success.
	_, flashCmd := p.Update(done)
	require.Empty(t, p.marks, "a fully-successful fanout drops all marks")
	require.NotNil(t, flashCmd)
	flash, ok := flashCmd().(footer.FlashShowMsg)
	require.True(t, ok)
	require.Equal(t, footer.FlashSuccess, flash.Level)
	require.Contains(t, flash.Text, "silenced 2 instances")
}
