// SPDX-License-Identifier: Apache-2.0

package pagetest

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// OpenedModal builds the modal a page command opens, so a test can name
// the confirmation level a key press asked for. Reflection is the only
// route from outside the app package: the open message is unexported on
// purpose, and exporting a reader for it would add a production API
// that no production caller needs.
func OpenedModal(tb testing.TB, cmd tea.Cmd) modal.Modal {
	tb.Helper()
	if cmd == nil {
		tb.Fatal("expected a command that opens a modal, got nil")
	}
	msg := cmd()
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Struct {
		tb.Fatalf("expected a modal-open message, got %T", msg)
	}
	field := v.FieldByName("Factory")
	if !field.IsValid() {
		tb.Fatalf("expected a modal-open message, got %T", msg)
	}
	factory, ok := reflect.TypeAssert[func() modal.Modal](field)
	if !ok {
		tb.Fatalf("expected a modal factory, got %T", field.Interface())
	}
	return factory()
}
