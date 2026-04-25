// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestNewRootCmd_Defaults(t *testing.T) {
	t.Parallel()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags)
	rootCmd.SetArgs([]string{})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	require.NoError(t, rootCmd.Execute())
	require.Equal(t, GlobalFlags{LogFormat: defaultLogFormat}, flags)
}

func TestNewRootCmd_FlagBinding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want GlobalFlags
	}{
		{
			name: "config-dir",
			args: []string{"--config-dir", "/tmp/cfg"},
			want: GlobalFlags{ConfigDir: "/tmp/cfg", LogFormat: defaultLogFormat},
		},
		{
			name: "log path and json format",
			args: []string{"--log", "/tmp/a.log", "--log-format", "json"},
			want: GlobalFlags{LogPath: "/tmp/a.log", LogFormat: "json"},
		},
		{
			name: "debug",
			args: []string{"--debug"},
			want: GlobalFlags{Debug: true, LogFormat: defaultLogFormat},
		},
		{
			name: "quiet alone",
			args: []string{"--quiet"},
			want: GlobalFlags{Quiet: true, LogFormat: defaultLogFormat},
		},
		{
			name: "read-only",
			args: []string{"--read-only"},
			want: GlobalFlags{ReadOnly: true, LogFormat: defaultLogFormat},
		},
		{
			name: "tenant subset",
			args: []string{"--tenant", "prod,staging"},
			want: GlobalFlags{Tenant: "prod,staging", LogFormat: defaultLogFormat},
		},
		{
			name: "tenant all",
			args: []string{"--tenant", "all"},
			want: GlobalFlags{Tenant: "all", LogFormat: defaultLogFormat},
		},
		{
			name: "poll interval",
			args: []string{"--poll-interval", "30s"},
			want: GlobalFlags{PollInterval: 30 * time.Second, LogFormat: defaultLogFormat},
		},
		{
			name: "theme",
			args: []string{"--theme", "gruvbox-dark"},
			want: GlobalFlags{Theme: "gruvbox-dark", LogFormat: defaultLogFormat},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var flags GlobalFlags
			rootCmd := newRootCmd(&flags)
			rootCmd.SetArgs(tc.args)
			rootCmd.SetOut(io.Discard)
			rootCmd.SetErr(io.Discard)

			require.NoError(t, rootCmd.Execute())
			require.Equal(t, tc.want, flags)
		})
	}
}

func TestNewRootCmd_DebugOverridesQuiet(t *testing.T) {
	t.Parallel()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags)
	rootCmd.SetArgs([]string{"--debug", "--quiet"})
	rootCmd.SetOut(io.Discard)
	var errBuf bytes.Buffer
	rootCmd.SetErr(&errBuf)

	require.NoError(t, rootCmd.Execute())
	require.True(t, flags.Debug)
	require.False(t, flags.Quiet, "--quiet must be reset when --debug is also set")
	require.Contains(t, errBuf.String(), "--debug overrides --quiet")
}

func TestNewRootCmd_UnknownFlagFails(t *testing.T) {
	t.Parallel()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags)
	rootCmd.SetArgs([]string{"--no-such-flag"})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	err := rootCmd.Execute()
	require.Error(t, err)
}

func TestNewRootCmd_HelpListsAllPersistentFlags(t *testing.T) {
	t.Parallel()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags)
	rootCmd.SetArgs([]string{"--help"})
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(io.Discard)

	require.NoError(t, rootCmd.Execute())
	out := outBuf.String()

	// Pull the expected flag set straight from the registered flags so
	// adding a new flag automatically extends this assertion and a
	// rename can never silently slip past the test.
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		require.Contains(t, out, "--"+f.Name, "help output missing flag --%s", f.Name)
	})
}

func TestNewRootCmd_BadDurationFails(t *testing.T) {
	t.Parallel()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags)
	rootCmd.SetArgs([]string{"--poll-interval", "notaduration"})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	require.Error(t, rootCmd.Execute())
}

func TestReconcileLogLevelFlags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		input    GlobalFlags
		want     GlobalFlags
		wantWarn bool
	}{
		{
			name:  "neither",
			input: GlobalFlags{},
			want:  GlobalFlags{},
		},
		{
			name:  "debug only",
			input: GlobalFlags{Debug: true},
			want:  GlobalFlags{Debug: true},
		},
		{
			name:  "quiet only",
			input: GlobalFlags{Quiet: true},
			want:  GlobalFlags{Quiet: true},
		},
		{
			name:     "both — debug wins",
			input:    GlobalFlags{Debug: true, Quiet: true},
			want:     GlobalFlags{Debug: true},
			wantWarn: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			flags := tc.input
			require.NoError(t, reconcileLogLevelFlags(&flags, &buf))
			require.Equal(t, tc.want, flags)
			if tc.wantWarn {
				require.Contains(t, buf.String(), "--debug overrides --quiet")
			} else {
				require.Empty(t, buf.String())
			}
		})
	}
}
