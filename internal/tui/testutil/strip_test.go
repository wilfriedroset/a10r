// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripStyle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text passes through", in: "hello world", want: "hello world"},
		{name: "single SGR is stripped", in: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "nested chained SGRs are stripped", in: "\x1b[1;38;2;255;0;0mbold-red\x1b[0m", want: "bold-red"},
		{name: "empty input", in: "", want: ""},
		{name: "only style yields empty", in: "\x1b[31m\x1b[0m", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, StripStyle(tc.in))
		})
	}
}
