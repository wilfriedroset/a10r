// SPDX-License-Identifier: Apache-2.0

package clipboard

import (
	"strings"
	"unicode"

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

// pickerValueWidth caps the value half of a picker row for display.
// The search corpus and Copy both use the value in full.
const pickerValueWidth = 60

// Field is one copyable value and the name the user searches for.
type Field struct {
	Name  string
	Value string
}

// Items renders the picker rows as "<name>: <value>" so one query
// narrows on the name or on the value.
func Items(fields []Field) []string {
	return rows(fields, func(v string) string { return format.Ellipsize(v, pickerValueWidth) })
}

func rows(fields []Field, shape func(string) string) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = format.SingleLine(f.Name) + ": " + shape(format.SingleLine(f.Value))
	}
	return out
}

// OpenPicker returns a Cmd that asks the App to push the single-select
// field picker over fields.
func OpenPicker(fields []Field) tea.Cmd {
	return app.OpenModal(func() modal.Modal { return newPicker(fields) })
}

func newPicker(fields []Field) *modal.Picker {
	return modal.NewPicker("Copy field", Items(fields), modal.PickerSingle).
		WithSearch(rows(fields, func(v string) string { return v })).
		WithOrigin(PickerOrigin)
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
		clip.Copy(pasteSafe(fields[i].Value)),
		footer.ShowFlash(footer.FlashInfo, "copied "+format.SingleLine(fields[i].Name)),
	)
}

// pasteSafe maps control runes to a space so a pasted value cannot end
// bracketed paste (ESC [201~) and run the rest as typed input. Newline
// and tab stay: the matchers block is newline-joined.
func pasteSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
