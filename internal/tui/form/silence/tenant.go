// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// pickerOrigin tags every PickerSubmittedMsg / PickerCancelledMsg
// the form's tenant picker emits. The App's lifecycle router only
// short-circuits picker results carrying app.PickerOriginScope
// (the Ctrl+T global picker) — everything else, including this
// tag, is forwarded to the top page so the form's Update consumes
// it. Originator-tagging beats a private wrapped message because
// the picker submit Cmd is fired by bubbletea directly into the
// App and the form has no intercept point before forwardToTop.
const pickerOrigin = "silence-form-tenant"

// openTenantPicker returns a Cmd that asks the App to push a
// single-select picker over every key in f.clients. The picker
// list is sorted alphabetically so the order is stable across
// runs / tests. Selection routes back as modal.PickerSubmittedMsg
// (handled in Update). The form deliberately passes the full map
// rather than intersecting with the page's scope: per ADR-0011
// scope is a viewing filter, deliberate writes shouldn't be gated
// by what the operator happens to be looking at.
func (f *Form) openTenantPicker() tea.Cmd {
	names := f.sortedTenantNames()
	return app.OpenModal(func() modal.Modal {
		return modal.NewPicker("Select tenant", names, modal.PickerSingle).
			WithOrigin(pickerOrigin)
	})
}

// sortedTenantNames returns f.clients keys in stable alphabetical
// order. Extracted from openTenantPicker so tests can assert the
// sort contract without reaching through the picker's modal envelope.
func (f *Form) sortedTenantNames() []string {
	names := make([]string, 0, len(f.clients))
	for t := range f.clients {
		names = append(names, t)
	}
	sort.Strings(names)
	return names
}

// tenantDisabled reports whether the Tenant row is read-only.
// Three independent triggers:
//   - bulk mode: row omitted entirely; checking the flag here
//     keeps the cycleFocus / Enter handler consistent with the
//     renderer's omission.
//   - editID set: edit mode can't move a silence between tenants.
//   - fewer than two clients: nothing meaningful to pick.
func (f *Form) tenantDisabled() bool {
	if f.bulk {
		return true
	}
	if f.editID != "" {
		return true
	}
	return len(f.clients) < 2
}
