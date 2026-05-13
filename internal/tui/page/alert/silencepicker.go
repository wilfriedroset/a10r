// SPDX-License-Identifier: Apache-2.0

package alert

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// silencePickerRow is one entry in the disambiguation modal: id is
// the silence's stable identifier (returned to the page on submit);
// line is the rendered single-line summary the user reads
// ("<id>  expires in 2h  by alice  — comment"). Decoupling the two
// avoids parsing the rendered line back into a UUID and keeps the
// renderer free to evolve.
type silencePickerRow struct {
	id   string
	line string
}

// SilenceSelectedMsg is the result of the silence-picker modal.
// Carries the silence ID the user picked. Implements modal.ResultMsg
// so the App auto-closes the modal when the message fires; sending
// a typed message — distinct from the App's hard-coded
// modal.PickerSubmittedMsg → ScopeChangedMsg branch — is what lets
// the alert detail page receive the selection instead of having it
// rerouted as a tenant scope change.
type SilenceSelectedMsg struct {
	ID string
}

// IsModalResult satisfies modal.ResultMsg.
func (SilenceSelectedMsg) IsModalResult() {}

// SilenceCancelledMsg is emitted on Esc or empty selection.
type SilenceCancelledMsg struct{}

// IsModalResult satisfies modal.ResultMsg.
func (SilenceCancelledMsg) IsModalResult() {}

// silencePicker wraps modal.Picker so the inner
// PickerSubmittedMsg/PickerCancelledMsg are translated into the
// typed result messages above. Necessary because app.go's modal-
// result branch hard-codes PickerSubmittedMsg to a tenant scope
// change — emitting the same type from a non-tenant picker would
// silently misroute. Picker stays generic; this wrapper costs ~30
// lines and one local index→id slice.
type silencePicker struct {
	inner *modal.Picker
	// idByIndex maps the inner picker's input position back to the
	// silence ID. Indexed (not keyed by rendered line) so two
	// silences with identical rendered lines (same expiry / creator /
	// comment, distinct IDs) don't collapse to one map entry and
	// drill into the wrong silence on submit.
	idByIndex []string
}

// newSilencePicker builds a wrapper over the supplied rows. The
// inner Picker is constructed with the rendered line strings (so
// fuzzy-match runs against what the user reads); idByIndex maps
// each input position back to the silence ID for the submit
// translation.
func newSilencePicker(rows []silencePickerRow) *silencePicker {
	items := make([]string, len(rows))
	idByIndex := make([]string, len(rows))
	for i, r := range rows {
		items[i] = r.line
		idByIndex[i] = r.id
	}
	return &silencePicker{
		inner:     modal.NewPicker("silences", items, modal.PickerSingle),
		idByIndex: idByIndex,
	}
}

// Init implements modal.Modal.
func (w *silencePicker) Init() tea.Cmd { return w.inner.Init() }

// Title implements modal.Modal — distinct from "tenants" so a
// future title-aware dispatcher could disambiguate even without the
// typed-result-msg trick this wrapper relies on.
func (*silencePicker) Title() string { return "silences" }

// Update implements modal.Modal. Forwards the message to the inner
// picker, then wraps any returned Cmd so the eventual result
// message is translated into our typed variants. The returned
// Modal is reseated when the inner picker hands back a different
// pointer — today modal.Picker always returns itself, but
// re-anchoring is cheap insurance against a future refactor that
// returns a derivative type.
func (w *silencePicker) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	inner, cmd := w.inner.Update(msg)
	if next, ok := inner.(*modal.Picker); ok {
		w.inner = next
	}
	if cmd == nil {
		return w, nil
	}
	translated := w.translate(cmd)
	return w, translated
}

// View implements modal.Modal.
func (w *silencePicker) View(width, height int) string {
	return w.inner.View(width, height)
}

// translate returns a Cmd that calls the inner picker's Cmd and
// rewrites its message: PickerSubmittedMsg → SilenceSelectedMsg
// (looked up via the inner picker's Indexes; defensive: fall through
// to cancelled if Indexes is empty or the index is out of range,
// which would mean the inner picker handed us a result we didn't
// construct it with), and PickerCancelledMsg → SilenceCancelledMsg.
// Other message shapes pass through unchanged so future picker
// behaviours don't get silently swallowed.
func (w *silencePicker) translate(inner tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		switch v := inner().(type) {
		case modal.PickerSubmittedMsg:
			if len(v.Indexes) == 0 {
				return SilenceCancelledMsg{}
			}
			idx := v.Indexes[0]
			if idx < 0 || idx >= len(w.idByIndex) {
				return SilenceCancelledMsg{}
			}
			return SilenceSelectedMsg{ID: w.idByIndex[idx]}
		case modal.PickerCancelledMsg:
			return SilenceCancelledMsg{}
		default:
			return v
		}
	}
}
