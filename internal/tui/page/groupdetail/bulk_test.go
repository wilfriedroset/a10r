// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/form/silence/silencetest"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/session"
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
		Session:   testutil.Session(),
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

// cappedMarksPage marks three instances under a max_bulk of two, the
// smallest shape that breaches the cap.
func cappedMarksPage(t *testing.T) *Page {
	t.Helper()
	p := cappedPage(t)
	for range 3 {
		_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Len(t, p.marks, 3)
	return p
}

// cappedPage is cappedMarksPage before the marks, so a test can reach
// the same breach through the visual range instead.
func cappedPage(t *testing.T) *Page {
	t.Helper()
	insts := make([]backend.Alert, 3)
	for i := range insts {
		insts[i] = instance(fmt.Sprintf("fp-%d", i), "warning", backend.AlertStateActive,
			map[string]string{sortKeyInstance: fmt.Sprintf("web-%d", i)})
	}
	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Clients:   map[string]silenceform.Client{tenant: &testutil.FakeSilenceClient{}},
		Instances: insts,
		Session: session.New(config.Config{Guardrails: guardrail.Set{{
			Tenants: []string{tenant},
			Actions: []string{"silence.create"},
			MaxBulk: new(2),
		}}}),
	})
	return p
}

// TestGuardrail_TheCapStopsTheBulkSilenceBeforeTheModal pins the cap
// rule on this page: the check runs before the confirm modal and
// leaves the marks set, so the user can narrow the selection and retry.
func TestGuardrail_TheCapStopsTheBulkSilenceBeforeTheModal(t *testing.T) {
	t.Parallel()

	p := cappedMarksPage(t)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok, "a capped run flashes instead of opening the confirm modal")
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Equal(t, "bulk silence on "+tenant+": 3 targets exceed max_bulk 2", msg.Text)
	require.Empty(t, p.pendingBulkSilence.targets, "nothing is queued for a write")
	require.Len(t, p.marks, 3, "the marks stay so the user can narrow them")
}

// TestGuardrail_AVisualRangeOverTheCapStopsThePress covers the range
// branch: the rows are not marked until `s` commits them, so the check
// has to run after the commit or a capped range reaches the modal.
func TestGuardrail_AVisualRangeOverTheCapStopsThePress(t *testing.T) {
	t.Parallel()

	p := cappedPage(t)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Empty(t, p.marks, "a range is not marked until the press commits it")

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok, "a capped range flashes instead of opening the confirm modal")
	require.Equal(t, "bulk silence on "+tenant+": 3 targets exceed max_bulk 2", msg.Text)
	require.Empty(t, p.pendingBulkSilence.targets, "nothing is queued for a write")
}

// TestGuardrail_ABreachedCapLeavesTheKeyOnTheHintStrip splits the run
// from the binding: a binding outlives any one run, so a cap the
// current marks happen to breach refuses the press without striking
// `s` off the hint strip.
func TestGuardrail_ABreachedCapLeavesTheKeyOnTheHintStrip(t *testing.T) {
	t.Parallel()

	p := cappedMarksPage(t)
	require.Empty(t, guardedKeys(p), "a breached cap refuses this run, not the binding")

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok)
	require.Equal(t, "bulk silence on "+tenant+": 3 targets exceed max_bulk 2", msg.Text)
}

// guardedKeys lists the keys the page currently reports as guarded, so
// a test names the whole outcome rather than one binding at a time.
func guardedKeys(p *Page) []string {
	var out []string
	for _, b := range p.Bindings() {
		if b.Guarded {
			out = append(out, b.Key)
		}
	}
	return out
}

// TestGuardrail_ATypedRuleReplacesTheBulkSilenceModal pins that, on
// this page, the rule strengthens the prompt the verb already has.
func TestGuardrail_ATypedRuleReplacesTheBulkSilenceModal(t *testing.T) {
	t.Parallel()

	insts := make([]backend.Alert, 2)
	for i := range insts {
		insts[i] = instance(fmt.Sprintf("fp-%d", i), "warning", backend.AlertStateActive,
			map[string]string{sortKeyInstance: fmt.Sprintf("web-%d", i)})
	}
	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Clients:   map[string]silenceform.Client{tenant: &testutil.FakeSilenceClient{}},
		Instances: insts,
		Session: session.New(config.Config{Guardrails: guardrail.Set{{
			Tenants:      []string{tenant},
			Actions:      []string{"silence.create"},
			Confirmation: guardrail.ConfirmationTypeTenantName,
		}}}),
	})
	for range insts {
		_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Len(t, p.marks, 2)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m := pagetest.OpenedModal(t, cmd)
	require.IsType(t, &modal.TypedConfirm{}, m)
	require.Contains(t, m.View(70, 14), `type "`+tenant+`" to confirm`)
}

// TestGuardrail_TheSilenceOneFormCarriesThePolicy pins that, on this
// page, the unmarked `s` pushes the form, and the form asks the write
// policy when it submits.
func TestGuardrail_TheSilenceOneFormCarriesThePolicy(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Clients:   map[string]silenceform.Client{tenant: &testutil.FakeSilenceClient{}},
		Instances: []backend.Alert{
			instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		},
		Session: session.New(config.Config{Guardrails: guardrail.Set{{
			Tenants:      []string{tenant},
			Actions:      []string{"silence.create"},
			Confirmation: guardrail.ConfirmationTypeTenantName,
		}}}),
	})

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.IsType(t, &modal.TypedConfirm{}, silencetest.SubmitModal(t, cmd, ""))
}

// TestGuardrail_APlainRuleAsksOnASingleMarkedInstance keeps the weaker
// level on the same route.
func TestGuardrail_APlainRuleAsksOnASingleMarkedInstance(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Clients:   map[string]silenceform.Client{tenant: &testutil.FakeSilenceClient{}},
		Instances: []backend.Alert{
			instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		},
		Session: session.New(config.Config{Guardrails: guardrail.Set{{
			Tenants:      []string{tenant},
			Actions:      []string{"silence.create"},
			Confirmation: guardrail.ConfirmationPlain,
		}}}),
	})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.IsType(t, &modal.Confirm{}, pagetest.OpenedModal(t, cmd))

	_, push := p.Update(modal.ConfirmResultMsg{Yes: true})
	require.IsType(t, &silenceform.Form{}, pagetest.PushedPage(t, push))
}

// TestGuardrail_ATypedRuleAsksOnASingleMarkedInstance closes the same
// one-target hole on this page: one mark skips the confirm modal, and
// the bulk form leaves policy to the page.
func TestGuardrail_ATypedRuleAsksOnASingleMarkedInstance(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Clients:   map[string]silenceform.Client{tenant: &testutil.FakeSilenceClient{}},
		Instances: []backend.Alert{
			instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		},
		Session: session.New(config.Config{Guardrails: guardrail.Set{{
			Tenants:      []string{tenant},
			Actions:      []string{"silence.create"},
			Confirmation: guardrail.ConfirmationTypeTenantName,
		}}}),
	})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Len(t, p.marks, 1)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m := pagetest.OpenedModal(t, cmd)
	require.IsType(t, &modal.TypedConfirm{}, m)
	require.Contains(t, m.View(70, 14), `type "`+tenant+`" to confirm`)
	require.NotContains(t, m.View(70, 14), "1 instances?", "one target reads as one instance")

	_, push := p.Update(modal.ConfirmResultMsg{Yes: true})
	require.IsType(t, &silenceform.Form{}, pagetest.PushedPage(t, push))
}
