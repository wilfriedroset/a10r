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

func TestHasBackground(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "plain text", in: "hello", want: false},
		{name: "reset only", in: "\x1b[mhi\x1b[m", want: false},
		{name: "named fg only", in: "\x1b[31mhi\x1b[m", want: false},
		{name: "truecolor fg only", in: "\x1b[38;2;108;112;134mhi\x1b[m", want: false},
		{name: "default bg reset is not a set bg", in: "\x1b[49mhi\x1b[m", want: false},
		{name: "fg rgb component matching a bg code", in: "\x1b[38;2;46;47;48mhi\x1b[m", want: false},
		{name: "truncated fg introducer at end", in: "\x1b[38mhi\x1b[m", want: false},
		{name: "malformed fg introducer then named bg", in: "\x1b[38;9;41mhi\x1b[m", want: true},
		{name: "named bg", in: "\x1b[41mhi\x1b[m", want: true},
		{name: "bright named bg", in: "\x1b[101mhi\x1b[m", want: true},
		{name: "truecolor bg only", in: "\x1b[48;2;30;30;46mhi\x1b[m", want: true},
		{name: "256-color bg", in: "\x1b[48;5;236mhi\x1b[m", want: true},
		{name: "combined fg and bg in one SGR", in: "\x1b[38;2;108;112;134;48;2;30;30;46mhi\x1b[m", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, HasBackground(tc.in))
		})
	}
}
