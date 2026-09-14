// SPDX-License-Identifier: Apache-2.0

package clipboard

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

// PickerOrigin tags every message the field picker emits. The App's
// lifecycle router forwards anything that is not the global scope
// picker to the top page, which gates on this tag so a foreign picker
// cannot drive a copy.
const PickerOrigin = "copyfield"

// pickerValueWidth caps the value half of a picker row. The row is
// also the picker's search corpus, so a query reaches only this far
// into a value; Copy still emits the value in full.
const pickerValueWidth = 60

// Field is one copyable value and the name the user searches for.
type Field struct {
	Name  string
	Value string
}

// Items renders the picker rows as "<name>: <value>" so one query
// narrows on the name or on the head of the value.
func Items(fields []Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		flat := strings.ReplaceAll(f.Value, "\n", " ")
		out[i] = f.Name + ": " + format.Ellipsize(flat, pickerValueWidth)
	}
	return out
}

// OpenPicker returns a Cmd that asks the App to push the single-select
// field picker over fields.
func OpenPicker(fields []Field) tea.Cmd {
	items := Items(fields)
	return app.OpenModal(func() modal.Modal {
		return modal.NewPicker("Copy field", items, modal.PickerSingle).
			WithOrigin(PickerOrigin)
	})
}

// CopySelected resolves a field-picker submit into the copy plus its flash.
// The value is read from fields by index, never from the submitted
// row, so a value cut for display still reaches the clipboard whole.
// An index outside fields is a silent no-op.
func CopySelected(clip Clipboard, fields []Field, m modal.PickerSubmittedMsg) tea.Cmd {
	if len(m.Indexes) == 0 {
		return nil
	}
	i := m.Indexes[0]
	if i < 0 || i >= len(fields) {
		return nil
	}
	return tea.Batch(
		clip.Copy(fields[i].Value),
		footer.ShowFlash(footer.FlashInfo, "copied "+fields[i].Name),
	)
}
