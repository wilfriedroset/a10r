// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/output"
)

func TestResolveDetailFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		tty     bool
		want    output.Format
		wantErr bool
	}{
		{name: "empty on tty yields yaml", raw: "", tty: true, want: output.FormatYAML},
		{name: "empty in pipe yields json", raw: "", tty: false, want: output.FormatJSON},
		{name: "explicit json passthrough", raw: "json", tty: true, want: output.FormatJSON},
		{name: "explicit yaml passthrough", raw: "yaml", tty: false, want: output.FormatYAML},
		{name: "explicit table is rejected", raw: "table", tty: false, wantErr: true},
		{name: "unknown format errors", raw: "xml", tty: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveDetailFormat(tc.raw, tc.tty)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	// The unknown-format error names only the formats this verb takes,
	// not the list commands' wider set (which includes table).
	_, err := resolveDetailFormat("xml", true)
	require.ErrorContains(t, err, "want json or yaml")
	// Matching is exact, like the list commands — uppercase is unknown.
	_, err = resolveDetailFormat("JSON", true)
	require.Error(t, err)
}

func TestRenderDetail_SingleVsSequence(t *testing.T) {
	t.Parallel()

	type row struct {
		Tenant string `json:"tenant"`
	}

	var single bytes.Buffer
	require.NoError(t, renderDetail(&single, output.FormatJSON, []row{{Tenant: "prod"}}))
	require.True(t, strings.HasPrefix(strings.TrimSpace(single.String()), "{"),
		"one match renders a single object, got %q", single.String())

	var multi bytes.Buffer
	require.NoError(t, renderDetail(&multi, output.FormatJSON, []row{{Tenant: "prod"}, {Tenant: "staging"}}))
	require.True(t, strings.HasPrefix(strings.TrimSpace(multi.String()), "["),
		"multiple matches render a sequence, got %q", multi.String())
}
