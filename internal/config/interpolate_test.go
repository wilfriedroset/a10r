// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterpolateBytes(t *testing.T) {
	t.Parallel()

	envSet := func(values map[string]string) func(string) string {
		return func(k string) string { return values[k] }
	}

	cases := []struct {
		name    string
		in      string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "no patterns leaves bytes unchanged",
			in:   "plain text with no env refs",
			want: "plain text with no env refs",
		},
		{
			name: "single set var substituted",
			in:   "token: ${FOO}",
			env:  map[string]string{"FOO": "bar"},
			want: "token: bar",
		},
		{
			name: "lowercase var name accepted",
			in:   "token: ${foo}",
			env:  map[string]string{"foo": "bar"},
			want: "token: bar",
		},
		{
			name:    "unset var without default errors",
			in:      "token: ${FOO}",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			// Pinned by docs in interpolate.go: `${FOO}` (no default)
			// requires a NON-EMPTY value, not just "set". Diverges from
			// strict POSIX on purpose — see the godoc rationale.
			name:    "empty var value treated as unset for ${FOO} form",
			in:      "token: ${FOO}",
			env:     map[string]string{"FOO": ""},
			wantErr: true,
		},
		{
			name: "value with metacharacters passes through verbatim",
			in:   "url: ${URL}",
			env:  map[string]string{"URL": "https://x:9090/path?a=b&c=d"},
			want: "url: https://x:9090/path?a=b&c=d",
		},
		{
			name: "default fallback used when var unset",
			in:   "token: ${FOO:-fallback}",
			env:  map[string]string{},
			want: "token: fallback",
		},
		{
			name: "default fallback used when var empty",
			in:   "token: ${FOO:-fallback}",
			env:  map[string]string{"FOO": ""},
			want: "token: fallback",
		},
		{
			name: "set value wins over default",
			in:   "token: ${FOO:-fallback}",
			env:  map[string]string{"FOO": "real"},
			want: "token: real",
		},
		{
			name: "empty default substitutes empty string",
			in:   "token: '${FOO:-}'",
			env:  map[string]string{},
			want: "token: ''",
		},
		{
			name: "default with spaces",
			in:   "token: ${FOO:-default with spaces}",
			env:  map[string]string{},
			want: "token: default with spaces",
		},
		{
			name: "multiple vars in one input",
			in:   "a=${A} b=${B:-fallback} c=${C}",
			env:  map[string]string{"A": "1", "C": "3"},
			want: "a=1 b=fallback c=3",
		},
		{
			name: "literal dollar sign untouched",
			in:   "price: $5 ${FOO}",
			env:  map[string]string{"FOO": "bar"},
			want: "price: $5 bar",
		},
		{
			name: "malformed pattern not matched",
			in:   "${ malformed }",
			env:  map[string]string{},
			want: "${ malformed }",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := interpolateBytes([]byte(tc.in), envSet(tc.env))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))
		})
	}
}

func TestInterpolateBytes_FirstUnresolvedNamesItself(t *testing.T) {
	t.Parallel()

	// Two unset vars: the error must name the FIRST one (top-down).
	// Helps the user fix configs incrementally instead of seeing a
	// generic "missing env" with no anchor.
	in := []byte("a=${ALPHA} b=${BETA}")
	env := func(string) string { return "" }

	_, err := interpolateBytes(in, env)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"ALPHA"`,
		"first unresolved var must be named in the error; got %v", err)
}
