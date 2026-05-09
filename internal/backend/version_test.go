// SPDX-License-Identifier: Apache-2.0

package backend

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		want    Version
		wantErr bool
	}{
		{name: "plain", input: "0.28.1", want: Version{0, 28, 1}},
		{name: "v-prefixed", input: "v0.28.1", want: Version{0, 28, 1}},
		{name: "rc suffix dropped", input: "0.28.1-rc.1", want: Version{0, 28, 1}},
		{name: "build-metadata dropped", input: "0.28.1+build.42", want: Version{0, 28, 1}},
		{name: "v-prefix + rc", input: "v0.28.1-alpha.2", want: Version{0, 28, 1}},
		{name: "two-component rejected", input: "0.28", wantErr: true},
		{name: "non-numeric major", input: "x.28.1", wantErr: true},
		{name: "non-numeric minor", input: "0.x.1", wantErr: true},
		{name: "non-numeric patch", input: "0.28.x", wantErr: true},
		{name: "empty rejected", input: "", wantErr: true},
		{name: "negative major rejected", input: "-1.0.0", wantErr: true},
		{name: "negative patch rejected", input: "0.0.-1", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseVersion(tc.input)
			if tc.wantErr {
				require.ErrorIs(t, err, ErrInvalidVersion)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestVersion_Compare(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    Version
		b    Version
		want int
	}{
		{name: "equal", a: Version{0, 28, 1}, b: Version{0, 28, 1}, want: 0},
		{name: "patch lower", a: Version{0, 28, 0}, b: Version{0, 28, 1}, want: -1},
		{name: "patch higher", a: Version{0, 28, 2}, b: Version{0, 28, 1}, want: 1},
		{name: "minor lower", a: Version{0, 27, 9}, b: Version{0, 28, 0}, want: -1},
		{name: "minor higher", a: Version{0, 29, 0}, b: Version{0, 28, 99}, want: 1},
		{name: "major lower", a: Version{0, 99, 99}, b: Version{1, 0, 0}, want: -1},
		{name: "major higher", a: Version{1, 0, 0}, b: Version{0, 99, 99}, want: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.a.Compare(tc.b))
		})
	}
}

func TestVersion_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0.28.1", Version{0, 28, 1}.String())
	require.Equal(t, "1.2.3", Version{1, 2, 3}.String())
}

func TestMinAlertmanagerVersion_Parses(t *testing.T) {
	t.Parallel()

	// The constant must always parse cleanly — a typo here would
	// cause every doctor invocation to error before running any
	// check.
	got, err := ParseVersion(MinAlertmanagerVersion)
	require.NoError(t, err)
	require.Equal(t, Version{0, 28, 1}, got)
}
