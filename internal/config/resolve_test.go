// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/log"
)

// envFromMap returns an EnvSource backed by the supplied map; missing
// keys yield "" (matching os.Getenv's contract).
func envFromMap(values map[string]string) EnvSource {
	return func(name string) string { return values[name] }
}

func TestResolve_LogPathPrecedence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cli  string
		env  map[string]string
		file string
		want string
	}{
		{name: "cli wins over env and file", cli: "/cli.log", env: map[string]string{envLog: "/env.log"}, file: "/file.log", want: "/cli.log"},
		{name: "env wins over file when cli empty", env: map[string]string{envLog: "/env.log"}, file: "/file.log", want: "/env.log"},
		{name: "file used when cli and env empty", file: "/file.log", want: "/file.log"},
		{name: "all empty leaves empty", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eff, err := Resolve(CLIFlags{LogPath: tc.cli}, envFromMap(tc.env), Config{Log: Log{Path: tc.file}})
			require.NoError(t, err)
			require.Equal(t, tc.want, eff.Config.Log.Path)
		})
	}
}

func TestResolve_LogFormatPrecedence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cli  string
		env  map[string]string
		file string
		want string
	}{
		{name: "cli wins", cli: "json", env: map[string]string{envLogFormat: "logfmt"}, file: "logfmt", want: "json"},
		{name: "env wins over file", env: map[string]string{envLogFormat: "json"}, file: "logfmt", want: "json"},
		{name: "file wins over default", file: "json", want: "json"},
		{name: "default fallback", want: string(log.FormatLogfmt)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eff, err := Resolve(CLIFlags{LogFormat: tc.cli}, envFromMap(tc.env), Config{Defaults: Defaults{LogFormat: tc.file}})
			require.NoError(t, err)
			require.Equal(t, tc.want, eff.Config.Defaults.LogFormat)
		})
	}
}

func TestResolve_ReadOnlyAnyTrueSourceWins(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cli  bool
		env  map[string]string
		file bool
		want bool
	}{
		{name: "no source false", want: false},
		{name: "cli only true", cli: true, want: true},
		{name: "file only true", file: true, want: true},
		{name: "env=true only", env: map[string]string{envReadOnly: "true"}, want: true},
		{name: "env=1 truthy", env: map[string]string{envReadOnly: "1"}, want: true},
		{name: "env=0 falsy", env: map[string]string{envReadOnly: "0"}, want: false},
		{name: "env=false falsy", env: map[string]string{envReadOnly: "false"}, want: false},
		{
			// ADR 0027: CLI true sticks even if env or config says false.
			// The resolver short-circuits on cli||file before consulting
			// env.
			name: "cli true overrides env=false",
			cli:  true,
			env:  map[string]string{envReadOnly: "false"},
			want: true,
		},
		{
			name: "file true overrides env=false",
			file: true,
			env:  map[string]string{envReadOnly: "false"},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eff, err := Resolve(CLIFlags{ReadOnly: tc.cli}, envFromMap(tc.env), Config{Defaults: Defaults{ReadOnly: tc.file}})
			require.NoError(t, err)
			require.Equal(t, tc.want, eff.Config.Defaults.ReadOnly)
		})
	}
}

func TestResolve_ReadOnlyGarbageEnvErrors(t *testing.T) {
	t.Parallel()

	// Pinning ADR 0027's "loud over silent" stance: a typoed
	// A10R_READ_ONLY=tru must surface, not be ignored. Asserting via
	// errors.Is keeps the contract independent of the exact wrapper
	// message format.
	_, err := Resolve(CLIFlags{}, envFromMap(map[string]string{envReadOnly: "tru"}), Config{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidReadOnlyEnv)
}

func TestResolve_ReadOnlyCLIShortCircuitsGarbageEnv(t *testing.T) {
	t.Parallel()

	// When --read-only is true, the resolver must never reach the env
	// parse branch, so a typoed A10R_READ_ONLY does not block an
	// explicitly-requested read-only session.
	eff, err := Resolve(
		CLIFlags{ReadOnly: true},
		envFromMap(map[string]string{envReadOnly: "tru"}),
		Config{},
	)
	require.NoError(t, err)
	require.True(t, eff.Config.Defaults.ReadOnly)
}

func TestResolve_ReadOnlyFileShortCircuitsGarbageEnv(t *testing.T) {
	t.Parallel()

	// Same short-circuit guarantee for the file source. Tightens the
	// contract that the resolver branches on cli||file BEFORE
	// inspecting env, so re-ordering the branches in the future would
	// fail this test.
	eff, err := Resolve(
		CLIFlags{},
		envFromMap(map[string]string{envReadOnly: "tru"}),
		Config{Defaults: Defaults{ReadOnly: true}},
	)
	require.NoError(t, err)
	require.True(t, eff.Config.Defaults.ReadOnly)
}

func TestResolve_PollIntervalCLIWinsOnlyOverDefault(t *testing.T) {
	t.Parallel()

	// ADR 0027: --poll-interval overrides defaults.poll_interval but
	// per-backend poll_interval values stay untouched. Resolve does
	// not touch Backends; the per-backend mix-in happens at the
	// backend factory.
	cli := 5 * time.Second
	cfg := Config{
		Defaults: Defaults{PollInterval: 30 * time.Second},
		Backends: []Backend{{Name: "b1", URL: "http://x", PollInterval: 90 * time.Second}},
	}

	eff, err := Resolve(CLIFlags{PollInterval: cli}, nil, cfg)
	require.NoError(t, err)
	require.Equal(t, cli, eff.Config.Defaults.PollInterval, "CLI must win for the global default")
	require.Equal(t, 90*time.Second, eff.Config.Backends[0].PollInterval,
		"per-backend value must NOT be overwritten by --poll-interval")
}

func TestResolve_PollIntervalFallbacks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cli  time.Duration
		file time.Duration
		want time.Duration
	}{
		{name: "cli wins", cli: 5 * time.Second, file: 30 * time.Second, want: 5 * time.Second},
		{name: "file wins when cli zero", file: 30 * time.Second, want: 30 * time.Second},
		{name: "default fallback", want: DefaultPollInterval},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eff, err := Resolve(
				CLIFlags{PollInterval: tc.cli},
				nil,
				Config{Defaults: Defaults{PollInterval: tc.file}},
			)
			require.NoError(t, err)
			require.Equal(t, tc.want, eff.Config.Defaults.PollInterval)
		})
	}
}

func TestResolve_ThemeFallbacks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cli  string
		file string
		want string
	}{
		{name: "cli wins", cli: "gruvbox-dark", file: "catppuccin-latte", want: "gruvbox-dark"},
		{name: "file wins when cli empty", file: "catppuccin-latte", want: "catppuccin-latte"},
		{name: "default fallback", want: DefaultThemeName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eff, err := Resolve(CLIFlags{Theme: tc.cli}, nil, Config{Theme: Theme{Name: tc.file}})
			require.NoError(t, err)
			require.Equal(t, tc.want, eff.Config.Theme.Name)
		})
	}
}

func TestResolve_RuntimeFlagsPropagated(t *testing.T) {
	t.Parallel()

	cli := CLIFlags{Debug: true, Quiet: false, Tenant: "prod,staging"}
	eff, err := Resolve(cli, nil, Config{})
	require.NoError(t, err)
	require.True(t, eff.Debug)
	require.False(t, eff.Quiet)
	require.Equal(t, "prod,staging", eff.Tenant)
}

func TestResolve_DoesNotMutateInputFile(t *testing.T) {
	t.Parallel()

	// Resolve takes file by value and the test confirms callers can
	// keep using their original Config without observing the resolver
	// having reached into it.
	original := Config{
		Defaults: Defaults{PollInterval: 30 * time.Second, ReadOnly: false},
		Theme:    Theme{Name: "catppuccin-latte"},
	}
	snapshot := original

	_, err := Resolve(CLIFlags{Theme: "gruvbox-dark", ReadOnly: true}, nil, original)
	require.NoError(t, err)
	require.Equal(t, snapshot, original, "Resolve must not mutate the caller's file Config")
}

func TestResolve_LogFormatDefaultMatchesLogPackage(t *testing.T) {
	t.Parallel()

	// Pin the cross-package default. internal/log.FormatLogfmt is the
	// canonical value; resolveLogFormat imports it directly so a
	// rename in the log package surfaces here at compile time, but a
	// runtime test guards against value drift in the rare case the
	// constant gets reassigned without renaming.
	eff, err := Resolve(CLIFlags{}, nil, Config{})
	require.NoError(t, err)
	require.Equal(t, "logfmt", eff.Config.Defaults.LogFormat,
		"resolved default log format must remain 'logfmt'")
}
