// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func TestModal_OpenSetsField(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	picker := modal.NewPicker("tenants", []string{"prod"}, modal.PickerSingle)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	require.NotNil(t, a.modal)
}

func TestModal_KeysCapturedBeforeDispatcher(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	picker := modal.NewPicker("tenants", []string{"prod"}, modal.PickerSingle)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	// `q` would normally quit at the global layer. With a modal
	// open, the modal swallows it (and ignores it because it's not
	// a recognised picker key).
	updated, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	a = updated.(*App)
	require.Nil(t, cmd, "modal must capture the key — Quit must NOT fire")
	require.NotNil(t, a.modal,
		"the modal must still be open: `q` is not a picker dismiss key")
}

func TestModal_EscDismissesModal(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	picker := modal.NewPicker("tenants", []string{"prod"}, modal.PickerSingle)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	// Esc inside an open modal closes the modal. The picker emits
	// a PickerCancelledMsg; the App processes it and clears the slot.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = updated.(*App)
	drive(t, a, cmd)
	require.Nil(t, a.modal, "Esc inside modal must close the modal")
}

func TestModal_SubmitTranslatesPickerToScopeChanged(t *testing.T) {
	t.Parallel()
	// The tenant picker is the only picker wired in v0.1. Its
	// submission translates to a ScopeChangedMsg so every list
	// page reacts the same way as for the `<0>` / `<1>`-`<9>`
	// numeric quick-switch — pages never see the raw picker
	// result.
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging"},
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))
	// PickerOriginScope mirrors what input.go's Ctrl+T binding sets
	// — the lifecycle router only translates picker submissions to
	// ScopeChangedMsg when the Origin matches.
	picker := modal.NewPicker("tenants", []string{"prod", "staging"}, modal.PickerSingle).
		WithOrigin(PickerOriginScope)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	// Cursor on row 0 ("prod"); Enter submits.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.Nil(t, a.modal, "submit must close the modal")
	require.NotEmpty(t, *page.updateLog)
	last := (*page.updateLog)[len(*page.updateLog)-1]
	scope, ok := last.(ScopeChangedMsg)
	require.Truef(t, ok,
		"page must see ScopeChangedMsg, not the raw picker result; got %T", last)
	require.Equal(t, "prod", scope.Scope)
}

func TestModal_ConfirmSubmitFlowsThrough(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("silences")
	drive(t, a, PushPage(func() Page { return page }))
	c := modal.NewConfirm("expire silence?", modal.ConfirmDefaultNo)
	drive(t, a, OpenModal(func() modal.Modal { return c }))

	updated, cmd := a.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	a = updated.(*App)
	drive(t, a, cmd)

	require.Nil(t, a.modal)
	last := (*page.updateLog)[len(*page.updateLog)-1]
	res, ok := last.(modal.ConfirmResultMsg)
	require.True(t, ok)
	require.True(t, res.Yes)
}

func TestModal_OpenWithNilFactoryIsNoOp(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	_, _ = a.Update(openModalMsg{Factory: nil})
	require.Nil(t, a.modal)
}

func TestModal_CloseModalCmd(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	picker := modal.NewPicker("tenants", []string{"prod"}, modal.PickerSingle)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))
	require.NotNil(t, a.modal)

	drive(t, a, CloseModal())
	require.Nil(t, a.modal)
}

func TestModal_RendersInBodySlot(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	// Push a page so the body would normally show "alerts body";
	// then open a modal and assert the modal text wins.
	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))
	picker := modal.NewPicker("pick a tenant", []string{"prod"}, modal.PickerSingle)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	visible := testutil.StripStyle(a.View().Content)
	require.Contains(t, visible, "pick a tenant",
		"open modal must replace the body slot")
	require.NotContains(t, visible, "alerts body",
		"page body must NOT bleed through under an open modal")
}

// Ensure flash messages still route correctly even with a modal open.
func TestModal_FlashStillRoutesWhileModalOpen(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	picker := modal.NewPicker("tenants", []string{"prod"}, modal.PickerSingle)
	drive(t, a, OpenModal(func() modal.Modal { return picker }))

	// FlashShowMsg arrives independent of the modal — the user
	// triggered something earlier whose Cmd resolved now.
	updated, _ = a.Update(footer.FlashShowMsg{Level: footer.FlashInfo, Text: "hi"})
	a = updated.(*App)
	require.True(t, a.flash.IsActive(),
		"FlashShowMsg must reach Flash even when a modal is open")
}
