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
// field validation. The arguments own opts.Clients, opts.Guardrails and
// opts.Tenant, because the tenant names the one client the map holds.
// Every other field is a default the caller can override on opts.
func guardedForm(t *testing.T, client Client, rules guardrail.Set, opts Options) *Form {
	t.Helper()
	opts.Clients = map[string]Client{defaultTenant: client}
	opts.Guardrails = rules
	opts.Tenant = defaultTenant
	if opts.Styles == nil {
		opts.Styles = testutil.LoadStyles(t)
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return fixedNow }
	}
	if opts.Creator == "" {
		opts.Creator = "alice"
	}
	if opts.Comment == "" {
		opts.Comment = "ack"
	}
	if opts.Matchers == nil {
		opts.Matchers = []backend.Matcher{{Name: "alertname", Value: "X", IsEqual: true}}
	}
	return New(opts)
}

// TestGuardrail_ADenyRefusesTheSubmit pins that a form submit
// is a write, so policy refuses it with the sentence every other
// surface prints.
func TestGuardrail_ADenyRefusesTheSubmit(t *testing.T) {
	t.Parallel()

	client := &fakeClient{}
	f := guardedForm(t, client, guardrail.Set{{
		Tenants: []string{defaultTenant},
		Actions: []string{"silence.create"},
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
		Actions: []string{"silence.expire"},
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
		Actions: []string{"silence.update"},
		Deny:    true,
	}}, Options{EditID: "sil-7", Action: guardrail.ActionSilenceUpdate})

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok, "a denied edit flashes, it does not write")
	require.Equal(t, "silence.update denied on prod", msg.Text)
	require.Equal(t, 0, client.calls())
}

// TestGuardrail_ATypedRulePromptsOnSubmit pins that the form
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

// TestGuardrail_ABulkSubmitNeverReachesTheGate pins the split: a bulk
// form collects metadata and the page owns the write, so the gate it
// already cleared is not asked a second time here. Every rule below
// names no tenant, so it matches whatever tenant the form holds and a
// green case cannot mean the rule simply missed.
func TestGuardrail_ABulkSubmitNeverReachesTheGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule guardrail.Rule
	}{
		{"deny", guardrail.Rule{Deny: true, Reason: "use the change ticket"}},
		{"typed confirmation", guardrail.Rule{Confirmation: guardrail.ConfirmationTypeTenantName}},
		{"plain confirmation", guardrail.Rule{Confirmation: guardrail.ConfirmationPlain}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeClient{}
			f := guardedForm(t, client, guardrail.Set{tt.rule}, Options{
				Bulk:   true,
				Action: guardrail.ActionSilenceCreate,
			})

			_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			require.NotNil(t, cmd)
			_, ok := cmd().(BulkSubmittedMsg)
			require.True(t, ok, "a bulk form still emits its metadata message")
			require.Equal(t, 0, client.calls())
		})
	}
}

// TestGuardrail_AConfirmedTenantSkipsThePrompt pins the hand-off: an
// answer the pushing page already collected clears the prompt for that
// backend and for no other, so one write asks once.
func TestGuardrail_AConfirmedTenantSkipsThePrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		confirmed []string
		wantWrite bool
	}{
		{"the form's own tenant", []string{defaultTenant}, true},
		{"another tenant", []string{"staging"}, false},
		{"nothing confirmed", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &fakeClient{wantID: "sil-1"}
			f := guardedForm(t, client, guardrail.Set{{
				Confirmation: guardrail.ConfirmationTypeTenantName,
			}}, Options{Confirmed: tt.confirmed})

			_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			require.NotNil(t, cmd)
			if !tt.wantWrite {
				require.IsType(t, &modal.TypedConfirm{}, pagetest.OpenedModal(t, cmd))
				require.Equal(t, 0, client.calls())
				return
			}
			_, ok := cmd().(submitDoneMsg)
			require.True(t, ok, "the carried answer releases the write")
			require.Equal(t, 1, client.createCalls)
		})
	}
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
