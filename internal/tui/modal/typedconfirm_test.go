// SPDX-License-Identifier: Apache-2.0

package modal

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"
)

func typeRunes(t *testing.T, m *TypedConfirm, s string) *TypedConfirm {
	t.Helper()
	for _, r := range s {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		tc, ok := next.(*TypedConfirm)
		require.True(t, ok, "TypedConfirm.Update must return *TypedConfirm")
		m = tc
	}
	return m
}

func TestTypedConfirm_ExactMatchConfirms(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire 3 silences", []string{"prod-eu"})
	m = typeRunes(t, m, "prod-eu")

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, ConfirmResultMsg{Yes: true}, cmd())
}

func TestTypedConfirm_MismatchKeepsThePromptOpen(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire 3 silences", []string{"prod-eu"})
	m = typeRunes(t, m, "prod-us")

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd, "a mismatch resolves nothing")
	m, ok := next.(*TypedConfirm)
	require.True(t, ok)
	require.Contains(t, m.View(60, 10), `type "prod-eu" to confirm`)
}

func TestTypedConfirm_EmptyEnterDoesNotConfirm(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire 3 silences", []string{"prod-eu"})

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
}

func TestTypedConfirm_BackspaceEditsTheBuffer(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire", []string{"prod-eu"})
	m = typeRunes(t, m, "prod-eux")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m, ok := next.(*TypedConfirm)
	require.True(t, ok)

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, ConfirmResultMsg{Yes: true}, cmd())
}

func TestTypedConfirm_EscCancels(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire", []string{"prod-eu", "prod-us"})
	m = typeRunes(t, m, "prod-eu")

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NotNil(t, cmd)
	require.Equal(t, ConfirmResultMsg{Cancelled: true}, cmd(),
		"one Esc cancels the whole run, whatever tenant it stands on")
}

// TestTypedConfirm_AsksEachTenantInTurn pins the bulk rule: a run that
// spans several restricted tenants confirms them one after another, and
// only the last match resolves the modal.
func TestTypedConfirm_AsksEachTenantInTurn(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire 5 silences", []string{"prod-eu", "prod-us"})
	require.Contains(t, m.View(60, 10), "prod-eu")

	m = typeRunes(t, m, "prod-eu")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd, "the first tenant advances rather than resolving")
	m, ok := next.(*TypedConfirm)
	require.True(t, ok)

	view := m.View(60, 10)
	require.Contains(t, view, "prod-us")
	require.NotContains(t, view, "prod-eu", "the buffer and the prompt move on")

	m = typeRunes(t, m, "prod-us")
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, ConfirmResultMsg{Yes: true}, cmd())
}

func TestTypedConfirm_Title(t *testing.T) {
	t.Parallel()

	require.Equal(t, "confirm", NewTypedConfirm("expire", []string{"prod-eu"}).Title())
}

// TestTypedConfirm_UppercaseIsTypable pins the terminal contract: a
// shifted letter arrives with ModShift set, so a backend name with a
// capital must still reach the buffer or its write is unreachable.
func TestTypedConfirm_UppercaseIsTypable(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire", []string{"Prod-EU"})
	for _, k := range []tea.KeyPressMsg{
		{Code: 'p', Text: "P", Mod: tea.ModShift},
		{Code: 'r', Text: "r"},
		{Code: 'o', Text: "o"},
		{Code: 'd', Text: "d"},
		{Code: '-', Text: "-"},
		{Code: 'e', Text: "E", Mod: tea.ModShift},
		{Code: 'u', Text: "U", Mod: tea.ModShift},
	} {
		next, _ := m.Update(k)
		tc, ok := next.(*TypedConfirm)
		require.True(t, ok)
		m = tc
	}

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, ConfirmResultMsg{Yes: true}, cmd())
}

// TestTypedConfirm_NoTenantsCancels pins the fail-closed rule: a prompt
// with nothing to ask for must never read a bare Enter as consent.
func TestTypedConfirm_NoTenantsCancels(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire", nil)

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, ConfirmResultMsg{Cancelled: true}, cmd())
}

func TestTypedConfirm_ViewShowsTheMismatchAndTheProgress(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire 5 silences", []string{"prod-eu", "prod-us"})
	m = typeRunes(t, m, "prod-us")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, ok := next.(*TypedConfirm)
	require.True(t, ok)

	view := m.View(60, 12)
	require.Contains(t, view, "that is not prod-eu")
	require.Contains(t, view, "tenant 1 of 2")
}

// TestTypedConfirm_EditClearsTheMismatch keeps the warning tied to the
// buffer the user just left behind, not to the one being retyped.
func TestTypedConfirm_EditClearsTheMismatch(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire", []string{"prod-eu"})
	m = typeRunes(t, m, "prod-us")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, ok := next.(*TypedConfirm)
	require.True(t, ok)
	require.Contains(t, m.View(60, 12), "that is not prod-eu")

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m, ok = next.(*TypedConfirm)
	require.True(t, ok)
	require.NotContains(t, m.View(60, 12), "that is not prod-eu")
}

// TestTypedConfirm_CtrlUClearsTheBuffer gives the same line-kill the
// picker offers, so a wrong tenant name is one key to drop.
func TestTypedConfirm_CtrlUClearsTheBuffer(t *testing.T) {
	t.Parallel()

	m := NewTypedConfirm("expire", []string{"prod-eu"})
	m = typeRunes(t, m, "prod-us")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m, ok := next.(*TypedConfirm)
	require.True(t, ok)
	require.NotContains(t, m.View(60, 12), "prod-us")

	m = typeRunes(t, m, "prod-eu")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, ConfirmResultMsg{Yes: true}, cmd())
}

func TestNewGuardedConfirm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		typed []string
		want  Modal
	}{
		{name: "no restricted tenant keeps the yes/no modal", typed: nil, want: &Confirm{}},
		{name: "one restricted tenant demands the typed prompt", typed: []string{"prod"}, want: &TypedConfirm{}},
		{name: "several restricted tenants demand the typed prompt", typed: []string{"prod", "prod-eu"}, want: &TypedConfirm{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewGuardedConfirm("expire silence sil-1?", ConfirmDefaultNo, tt.typed)
			require.IsType(t, tt.want, got)
			require.Contains(t, got.View(60, 12), "expire silence sil-1?")
		})
	}
}
