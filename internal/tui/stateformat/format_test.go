// SPDX-License-Identifier: Apache-2.0

package stateformat_test

import (
	"testing"

	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
)

func TestFormat_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		f    stateformat.Format
		want string
	}{
		{name: "zero value is full", f: stateformat.Format(0), want: "full"},
		{name: "full", f: stateformat.Full, want: "full"},
		{name: "compact", f: stateformat.Compact, want: "compact"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.f.String(); got != tt.want {
				t.Errorf("Format.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormat_ZeroIsFull(t *testing.T) {
	t.Parallel()
	var f stateformat.Format
	if f != stateformat.Full {
		t.Errorf("zero value = %v, want Full so a page opens in the legible default", f)
	}
}
