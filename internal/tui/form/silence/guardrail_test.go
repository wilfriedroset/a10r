// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// guardedForm builds a ready-to-submit form on tenant "prod" under the
// given policy, so each test below reads the guardrail gate and not the
// field validation.
func guardedForm(t *testing.T, client Client, rules guardrail.Set, opts Options) *Form {
	t.Helper()
	opts.Clients = map[string]Client{defaultTenant: client}
	opts.Tenant = defaultTenant
	opts.Styles = testutil.LoadStyles(t)
	opts.Now = func() time.Time { return fixedNow }
	opts.Creator = "alice"
	opts.Comment = "ack"
	opts.Guardrails = rules
	if opts.Matchers == nil {
		opts.Matchers = []backend.Matcher{{Name: "alertname", Value: "X", IsEqual: true}}
	}
	return New(opts)
}

// TestGuardrail_ADenyRefusesTheSubmit pins spec item 13: a form submit
// is a write, so policy refuses it with the sentence every other
// surface prints.
func TestGuardrail_ADenyRefusesTheSubmit(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	f := guardedForm(t, client, guardrail.Set{{
		Tenants: []string{defaultTenant},
		Actions: []string{guardrail.ActionSilenceCreate},
		Deny:    true,
		Reason:  "use the change ticket",
	}}, Options{})

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok, "a denied submit flashes, it does not write")
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Equal(t, "silence.create denied on prod: use the change ticket", msg.Text)
	require.Equal(t, 0, client.calls(), "nothing reaches the backend")
}

// TestGuardrail_ADenyOnAnotherVerbLeavesTheSubmitAlone keeps the gate
// on the verb the form performs.
func TestGuardrail_ADenyOnAnotherVerbLeavesTheSubmitAlone(t *testing.T) {
	t.Parallel()

	client := &fakeClient{wantID: "sil-1"}
	f := guardedForm(t, client, guardrail.Set{{
		Tenants: []string{defaultTenant},
		Actions: []string{guardrail.ActionSilenceExpire},
		Deny:    true,
	}}, Options{})

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	_, ok := cmd().(submitDoneMsg)
	require.True(t, ok, "an unrelated deny must not touch the create path")
	require.Equal(t, 1, client.createCalls)
}

// TestGuardrail_AnEditSubmitReadsTheUpdateVerb pins that the form names
// the verb it really performs: an edit is silence.update.
func TestGuardrail_AnEditSubmitReadsTheUpdateVerb(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	f := guardedForm(t, client, guardrail.Set{{
		Tenants: []string{defaultTenant},
		Actions: []string{guardrail.ActionSilenceUpdate},
		Deny:    true,
	}}, Options{EditID: "sil-7", Action: guardrail.ActionSilenceUpdate})

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok, "a denied edit flashes, it does not write")
	require.Equal(t, "silence.update denied on prod", msg.Text)
	require.Equal(t, 0, client.calls())
}

// TestGuardrail_ATypedRulePromptsOnSubmit pins spec item 13: the form
// has no confirmation today, so the rule adds one at the submit.
func TestGuardrail_ATypedRulePromptsOnSubmit(t *testing.T) {
	t.Parallel()

	client := &fakeClient{wantID: "sil-1"}
	f := guardedForm(t, client, guardrail.Set{{
		Tenants:      []string{defaultTenant},
		Confirmation: guardrail.ConfirmationTypeTenantName,
	}}, Options{})

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m := pagetest.OpenedModal(t, cmd)
	require.IsType(t, &modal.TypedConfirm{}, m)
	require.Contains(t, m.View(60, 12), `type "prod" to confirm`)
	require.Equal(t, 0, client.calls(), "the write waits for the answer")

	_, cmd = f.Update(modal.ConfirmResultMsg{Yes: true})
	require.NotNil(t, cmd)
	_, ok := cmd().(submitDoneMsg)
	require.True(t, ok, "the cleared prompt releases the write")
	require.Equal(t, 1, client.createCalls)
}

// TestGuardrail_ACancelledPromptKeepsTheFormOpen pins that Esc on the
// typed prompt loses nothing the user typed and writes nothing.
func TestGuardrail_ACancelledPromptKeepsTheFormOpen(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	f := guardedForm(t, client, guardrail.Set{{
		Tenants:      []string{defaultTenant},
		Confirmation: guardrail.ConfirmationTypeTenantName,
	}}, Options{})
	_, _ = f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})

	_, cmd := f.Update(modal.ConfirmResultMsg{Cancelled: true})
	require.Nil(t, cmd, "a cancelled prompt is silent")
	require.Equal(t, 0, client.calls())

	_, cmd = f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.IsType(t, &modal.TypedConfirm{}, pagetest.OpenedModal(t, cmd),
		"the next submit asks again")
}

// TestGuardrail_ABulkFormLeavesThePolicyToThePage pins the split: a
// bulk form collects metadata and the page owns the write, so the gate
// it already cleared is not asked a second time here.
func TestGuardrail_ABulkFormLeavesThePolicyToThePage(t *testing.T) {
	t.Parallel()

	f := guardedForm(t, &fakeClient{}, guardrail.Set{{
		Tenants:      []string{defaultTenant},
		Deny:         true,
		Confirmation: guardrail.ConfirmationTypeTenantName,
	}}, Options{Bulk: true})

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	_, ok := cmd().(BulkSubmittedMsg)
	require.True(t, ok, "a bulk form still emits its metadata message")
}

// TestGuardrail_APlainRuleAsksTheYesNoQuestion pins that the form
// honours the weaker level too. A single create confirms nothing
// today, so a plain rule is the only thing standing between the key
// and the write.
func TestGuardrail_APlainRuleAsksTheYesNoQuestion(t *testing.T) {
	t.Parallel()

	client := &fakeClient{wantID: "sil-1"}
	f := guardedForm(t, client, guardrail.Set{{
		Tenants:      []string{defaultTenant},
		Confirmation: guardrail.ConfirmationPlain,
	}}, Options{})

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.IsType(t, &modal.Confirm{}, pagetest.OpenedModal(t, cmd))
	require.Equal(t, 0, client.calls(), "the write waits for the answer")

	_, cmd = f.Update(modal.ConfirmResultMsg{Yes: true})
	require.NotNil(t, cmd)
	_, ok := cmd().(submitDoneMsg)
	require.True(t, ok, "the answered question releases the write")
	require.Equal(t, 1, client.createCalls)
}

// TestGuardrail_ATenantChangeAsksAgain pins why the cleared answer is
// remembered by name rather than by a flag: a failed write leaves the
// form open, the Tenant row can move the retry to another backend, and
// that backend has its own rule.
func TestGuardrail_ATenantChangeAsksAgain(t *testing.T) {
	t.Parallel()

	client := &fakeClient{wantErr: errors.New("boom")}
	other := &fakeClient{wantID: "sil-2"}
	f := guardedForm(t, client, guardrail.Set{{
		Confirmation: guardrail.ConfirmationTypeTenantName,
	}}, Options{})
	f.clients["staging"] = other

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.IsType(t, &modal.TypedConfirm{}, pagetest.OpenedModal(t, cmd))
	_, write := f.Update(modal.ConfirmResultMsg{Yes: true})
	require.NotNil(t, write)
	_, _ = f.Update(write())
	require.Equal(t, 1, client.createCalls, "the cleared prompt released the write")

	_, _ = f.Update(modal.PickerSubmittedMsg{Origin: pickerOrigin, Selections: []string{"staging"}})
	_, cmd = f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m := pagetest.OpenedModal(t, cmd)
	require.IsType(t, &modal.TypedConfirm{}, m)
	require.Contains(t, m.View(60, 12), `type "staging" to confirm`)
	require.Equal(t, 0, other.calls(), "the new backend waits for its own answer")
}

// TestGuardrail_AStrayConfirmStartsNoWrite pins the fail-closed side of
// the prompt: the form answers its own question only, so a confirm
// result it never asked for cannot start a write.
func TestGuardrail_AStrayConfirmStartsNoWrite(t *testing.T) {
	t.Parallel()

	client := &fakeClient{wantID: "sil-1"}
	f := guardedForm(t, client, nil, Options{})

	_, cmd := f.Update(modal.ConfirmResultMsg{Yes: true})
	require.Nil(t, cmd)
	require.Equal(t, 0, client.calls())
}
