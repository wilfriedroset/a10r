// SPDX-License-Identifier: Apache-2.0

package pagetest

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// OpenedModal builds the modal a page command opens, so a test can name
// the confirmation level a key press asked for.
func OpenedModal(tb testing.TB, cmd tea.Cmd) modal.Modal {
	tb.Helper()
	m, ok := factoryOutput(tb, cmd).(modal.Modal)
	if !ok {
		tb.Fatal("expected a modal-open command, got a page push")
	}
	return m
}

// PushedPage builds the page a command pushes, so a test can drive the
// pushed page itself. The result is untyped because app.Page lives in
// the package this one is deliberately kept out of, so callers assert
// the concrete page type they expect.
func PushedPage(tb testing.TB, cmd tea.Cmd) any {
	tb.Helper()
	return factoryOutput(tb, cmd)
}

// factoryOutput runs the factory an app open or push message carries.
// Reflection is the only route from outside the app package: both
// messages are unexported on purpose, and exporting a reader for them
// would add a production API that no production caller needs. Both name
// the field Factory, and the two pin tests in the app package keep
// that name next to the files that own it.
func factoryOutput(tb testing.TB, cmd tea.Cmd) any {
	tb.Helper()
	if cmd == nil {
		tb.Fatal("expected a command carrying a factory, got nil")
	}
	msg := cmd()
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Struct {
		tb.Fatalf("expected an app open or push message, got %T", msg)
	}
	field := v.FieldByName("Factory")
	if !field.IsValid() || field.Kind() != reflect.Func || field.IsNil() || field.Type().NumIn() != 0 {
		tb.Fatalf("expected an app open or push message, got %T", msg)
	}
	out := reflect.ValueOf(field.Interface()).Call(nil)
	if len(out) != 1 {
		tb.Fatalf("expected a factory returning one value, got %d", len(out))
	}
	return out[0].Interface()
}
