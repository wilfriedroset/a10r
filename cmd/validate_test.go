// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
)

func writeYAML(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestRunValidate_GoodConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeYAML(t, dir, "a10r.yaml", "backends:\n  - name: ok\n    url: http://x\n")

	var buf bytes.Buffer
	flags := &GlobalFlags{ConfigDir: dir}
	err := runValidate(&buf, flags, nil)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "config valid")
	require.Contains(t, buf.String(), "1 backend(s) configured")
}

func TestRunValidate_PositionalPathOverridesConfigDir(t *testing.T) {
	t.Parallel()

	// --config-dir points at an empty dir; the positional arg points
	// at a real file under a different dir. The positional must win.
	emptyDir := t.TempDir()
	otherDir := t.TempDir()
	target := writeYAML(t, otherDir, "alt.yaml", "backends:\n  - name: pos\n    url: http://x\n")

	var buf bytes.Buffer
	flags := &GlobalFlags{ConfigDir: emptyDir}
	err := runValidate(&buf, flags, []string{target})
	require.NoError(t, err)
	require.Contains(t, buf.String(), "1 backend(s)")
}

func TestRunValidate_MissingFileReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	flags := &GlobalFlags{ConfigDir: dir}
	err := runValidate(io.Discard, flags, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, config.ErrNotFound,
		"validate must surface ErrNotFound — pipelines treat exit-non-zero as failure")
	// Per ADR 0009 the missing file case is a config error → exit 2.
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
}

func TestRunValidate_ParseError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeYAML(t, dir, "a10r.yaml", "backends:\n  - name: x\n    url: http://x\n    pollInterval: 30s\n")

	flags := &GlobalFlags{ConfigDir: dir}
	err := runValidate(io.Discard, flags, nil)
	require.Error(t, err, "strict-mode rejects unknown fields")
	require.NotErrorIs(t, err, config.ErrNotFound)
	// Same exit code as missing file — both are config-side failures.
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
}

func TestLoadOptsFromArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		flags *GlobalFlags
		args  []string
		want  config.LoadOpts
	}{
		{
			name:  "no args uses --config-dir",
			flags: &GlobalFlags{ConfigDir: "/explicit/dir"},
			want:  config.LoadOpts{Dir: "/explicit/dir"},
		},
		{
			name:  "single arg splits into dir+file",
			flags: &GlobalFlags{},
			args:  []string{"/etc/a10r/staging.yaml"},
			want:  config.LoadOpts{Dir: "/etc/a10r", File: "staging.yaml"},
		},
		{
			name:  "single arg overrides --config-dir",
			flags: &GlobalFlags{ConfigDir: "/should/be/ignored"},
			args:  []string{"/path/to/file.yaml"},
			want:  config.LoadOpts{Dir: "/path/to", File: "file.yaml"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, loadOptsFromArgs(tc.flags, tc.args))
		})
	}
}
