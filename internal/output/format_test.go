// SPDX-License-Identifier: Apache-2.0

package output

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		want    Format
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "table", input: "table", want: FormatTable},
		{name: "json", input: "json", want: FormatJSON},
		{name: "yaml", input: "yaml", want: FormatYAML},
		{name: "csv unsupported", input: "csv", wantErr: true},
		{name: "uppercase rejected", input: "JSON", wantErr: true},
		{name: "mixed case rejected", input: "Json", wantErr: true},
		{name: "trailing space rejected", input: "json ", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseFormat(tc.input)
			if tc.wantErr {
				require.ErrorIs(t, err, ErrUnknownFormat)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		format    Format
		ttyStdout bool
		want      Format
	}{
		{name: "explicit table on tty", format: FormatTable, ttyStdout: true, want: FormatTable},
		{name: "explicit json on tty", format: FormatJSON, ttyStdout: true, want: FormatJSON},
		{name: "explicit yaml on pipe", format: FormatYAML, ttyStdout: false, want: FormatYAML},
		{name: "empty + tty defaults to table", format: "", ttyStdout: true, want: FormatTable},
		{name: "empty + pipe defaults to json", format: "", ttyStdout: false, want: FormatJSON},
		// Garbage passthrough is the documented contract: Resolve
		// trusts that the value came out of ParseFormat. The test
		// pins this so a future "defensive defaulting" refactor
		// would have to revisit the contract intentionally.
		{name: "garbage passes through unchanged", format: "csv", ttyStdout: true, want: "csv"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, Resolve(tc.format, tc.ttyStdout))
		})
	}
}
