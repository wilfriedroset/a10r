// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// TestModal_NonScopePickerForwardsToTopPage is the integration-shape
// regression test for the silence form's tenant picker round-trip.
// Before the Origin tag was wired, the App's lifecycle handler
// translated *every* PickerSubmittedMsg into a global ScopeChangedMsg
// and never forwarded it to the top page — so the silence form (which
// pushes its own tenant picker on Enter from the Tenant row) could
// never react to the user's selection in production, even though the
// form's unit tests passed by injecting the message directly.
//
// This test simulates that path end-to-end at the App boundary:
// push a fakePage, open a picker tagged with a non-scope Origin
// (mimicking what silence/form.go's openTenantPicker does), drive
// Enter, and assert (a) no ScopeChangedMsg is emitted by the App
// and (b) the original picker submission lands on the top page so
// the page's Update can consume it.
func TestModal_NonScopePickerForwardsToTopPage(t *testing.T) {
	t.Parallel()

	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging"},
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("silence-form")
	drive(t, a, PushPage(func() Page { return page }))

	// Open a picker tagged with a non-scope Origin — this mirrors
	// the silence form's openTenantPicker, which stamps its own
	// pickerOrigin tag. The literal "silence-form-tenant" string
	// matches the form's package-private const; we hard-code it
	// here because the app package must not import the form
	// package (cycle), and the contract under test is the App's
	// "only Origin==scope short-circuits" behaviour.
	const formOrigin = "silence-form-tenant"
	picker := modal.NewPicker("Select tenant", []string{"prod", "staging"}, modal.PickerSingle).
		WithOrigin(formOrigin)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	// Cursor on row 0 ("prod"); Enter submits.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.Nil(t, a.modal, "submit must close the modal")

	// The page must have seen the picker result — that's the
	// integration path the prior wiring broke.
	require.NotEmpty(t, *page.updateLog,
		"top page must receive the non-scope picker submission")
	last := (*page.updateLog)[len(*page.updateLog)-1]
	pm, ok := last.(modal.PickerSubmittedMsg)
	require.Truef(t, ok,
		"top page must see PickerSubmittedMsg, not a translated message; got %T", last)
	require.Equal(t, formOrigin, pm.Origin,
		"the Origin tag must round-trip so the page can confirm it's the addressee")
	require.Equal(t, []string{"prod"}, pm.Selections,
		"selection must round-trip unchanged")

	// No ScopeChangedMsg may appear anywhere in the page's update
	// log — the App must not translate a foreign-Origin picker into
	// a scope change.
	for _, m := range *page.updateLog {
		_, isScope := m.(ScopeChangedMsg)
		require.Falsef(t, isScope,
			"App must NOT translate a non-scope picker submit into ScopeChangedMsg; got %#v", m)
	}
}

// TestModal_NonScopePickerCancelForwardsToTopPage mirrors the submit
// test for the cancel path: Esc inside a non-scope picker must reach
// the top page so the originator (e.g. the silence form) can decide
// whether to react. The previous wiring swallowed the cancel at the
// App layer the same way as submissions.
func TestModal_NonScopePickerCancelForwardsToTopPage(t *testing.T) {
	t.Parallel()

	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging"},
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("silence-form")
	drive(t, a, PushPage(func() Page { return page }))

	const formOrigin = "silence-form-tenant"
	picker := modal.NewPicker("Select tenant", []string{"prod", "staging"}, modal.PickerSingle).
		WithOrigin(formOrigin)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = updated.(*App)
	drive(t, a, cmd)

	require.Nil(t, a.modal, "Esc must close the modal")
	require.NotEmpty(t, *page.updateLog)
	last := (*page.updateLog)[len(*page.updateLog)-1]
	pc, ok := last.(modal.PickerCancelledMsg)
	require.Truef(t, ok,
		"top page must receive PickerCancelledMsg for a non-scope cancel; got %T", last)
	require.Equal(t, formOrigin, pc.Origin,
		"Origin must round-trip on cancel too so the page can verify ownership")
}

// TestModal_ScopePickerStillTranslates pins the inverse contract:
// the global Ctrl+T picker tagged with PickerOriginScope must still
// fold into a ScopeChangedMsg the way TestModal_SubmitTranslatesPickerToScopeChanged
// already covers, even with the new Origin gating in place. Adding
// this here keeps the two-branch behaviour visible in one file.
func TestModal_ScopePickerStillTranslates(t *testing.T) {
	t.Parallel()

	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging"},
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	picker := modal.NewPicker("tenants", []string{"prod", "staging"}, modal.PickerSingle).
		WithOrigin(PickerOriginScope)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.Nil(t, a.modal)
	require.NotEmpty(t, *page.updateLog)
	last := (*page.updateLog)[len(*page.updateLog)-1]
	_, ok := last.(ScopeChangedMsg)
	require.Truef(t, ok,
		"scope-origin picker submit must translate to ScopeChangedMsg; got %T", last)
	// The raw picker message must NOT have leaked through alongside
	// the translation — otherwise pages would see both events.
	for _, m := range *page.updateLog {
		_, isPicker := m.(modal.PickerSubmittedMsg)
		require.Falsef(t, isPicker,
			"scope-origin picker submit must NOT also forward the raw picker message; got %#v", m)
	}
}
