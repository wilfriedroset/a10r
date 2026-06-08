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
