// SPDX-License-Identifier: Apache-2.0

package browser

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenerSelectsPerGOOS(t *testing.T) {
	t.Parallel()

	const url = "https://example.test/graph"
	for _, tc := range []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "open", []string{url}},
		{"windows", "cmd", []string{"/c", "start", "", url}},
		{"linux", xdgOpen, []string{url}},
		{"freebsd", xdgOpen, []string{url}},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			t.Parallel()
			name, args := opener(tc.goos, url)
			require.Equal(t, tc.wantName, name)
			require.Equal(t, tc.wantArgs, args)
		})
	}
}

func TestSystemOpenRoutesThroughRunner(t *testing.T) {
	t.Parallel()

	var gotName string
	var gotArgs []string
	s := System{run: func(name string, args ...string) error {
		gotName = name
		gotArgs = args
		return nil
	}}
	require.NoError(t, s.Open("https://example.test"))
	require.NotEmpty(t, gotName)
	require.Contains(t, gotArgs, "https://example.test")
}

func TestSystemOpenSurfacesRunnerError(t *testing.T) {
	t.Parallel()

	want := errors.New("exec: \"xdg-open\": executable file not found in $PATH")
	s := System{run: func(string, ...string) error { return want }}
	require.ErrorIs(t, s.Open("https://example.test"), want)
}
