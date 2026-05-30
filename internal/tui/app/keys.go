// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// normalizeKey converts a tea.KeyPressMsg into the binding-string
// format used by keybindings.md and the keys.Dispatcher
// ("Ctrl+S", "Shift+G", "Esc", "Enter", "j", "g"). bubbletea v2
// emits lowercase formats ("ctrl+s", "esc"); the dispatcher's tests
// pin the title-case spellings, so the App owns this translation
// rather than baking format choice into the dispatcher.
//
// Modifier order is canonical Ctrl > Alt > Shift, e.g.
// "Ctrl+Shift+S". The order is fixed here so every binding has
// one true spelling and the dispatcher's Set keys agree with
// the dispatched names.
//
// The normalizer is intentionally narrow: it handles the keys
// keybindings.md actually binds. Function keys, mouse, and other
// special events return "" so the caller can let them fall through
// to the dispatcher's miss path.
func normalizeKey(k tea.KeyPressMsg) string {
	key := k.Key()

	special := specialName(key.Code)

	// Modifier-only events have no Code — drop them.
	if special == "" && key.Code == 0 {
		return ""
	}

	if special == "" {
		// A printable rune. Use the lower-case Code for the binding
		// name; Shift+letter is reported separately as "Shift+X" via
		// the modifier path so a plain `g` stays `"g"` for the chord
		// dispatcher, while a shifted `G` becomes `"Shift+G"`.
		if !unicode.IsPrint(key.Code) {
			return ""
		}
		special = string(key.Code)
	}

	mods := key.Mod
	if mods == 0 {
		return special
	}

	var prefix []string
	if mods&tea.ModCtrl != 0 {
		prefix = append(prefix, "Ctrl")
	}
	if mods&tea.ModAlt != 0 {
		prefix = append(prefix, "Alt")
	}
	if mods&tea.ModShift != 0 {
		prefix = append(prefix, "Shift")
	}
	// Per keybindings.md: every modifier+letter binding is written
	// with an uppercase letter ("Ctrl+S", "Shift+G", "Alt+X") so the
	// table stays parseable. Special key names ("Esc", "Enter") are
	// already title-cased by specialName so this is idempotent.
	prefix = append(prefix, strings.ToUpper(special))
	return strings.Join(prefix, "+")
}

// specialNames is the lookup table from a non-printable key code
// to the binding-table label keybindings.md uses. Reading the map
// literal lines up visually with the keybindings.md catalog, which
// is the point.
var specialNames = map[rune]string{
	tea.KeyEnter:     "Enter",
	tea.KeyEscape:    keyNameEsc,
	tea.KeyTab:       "Tab",
	tea.KeyBackspace: "Backspace",
	tea.KeySpace:     "Space",
	tea.KeyUp:        "Up",
	tea.KeyDown:      "Down",
	tea.KeyLeft:      "Left",
	tea.KeyRight:     "Right",
	tea.KeyHome:      "Home",
	tea.KeyEnd:       "End",
	tea.KeyPgUp:      "PgUp",
	tea.KeyPgDown:    "PgDown",
	tea.KeyDelete:    "Delete",
	tea.KeyInsert:    "Insert",
}

// specialName returns the binding-table label for the given key
// code, or "" if the code is a printable rune or an unmapped
// special key (function keys etc.) — callers fall back to rune-
// based naming for the printable case.
func specialName(code rune) string { return specialNames[code] }
