// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/boot"
)

// runSnapshotCmd drives the real `a10r snapshot` command against a
// backend that refuses every connection, so the frame renders the
// degraded band without a network and without a terminal. Only
// stdout is returned, because ADR 0045 makes stdout the whole
// contract: the frame, and nothing beside it.
func runSnapshotCmd(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	dir := t.TempDir()
	// Keep prompt-history and state writes inside the test's own
	// tree rather than the developer's real XDG dirs.
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))

	cfgPath := filepath.Join(dir, "a10r.yaml")
	// Port 1 is reserved and unbound, so the poller fails fast and
	// the render does not spend its whole wait on a live socket.
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"backends:\n  - name: prod\n    url: http://127.0.0.1:1\n",
	), 0o600))

	var flags GlobalFlags
	root := newRootCmd(&flags, func(*cobra.Command, *GlobalFlags) error { return nil })
	registerSubcommands(root, &flags)

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{
		snapshotUse,
		"--config", cfgPath,
		"--config-dir", filepath.Join(dir, "config"),
		"--log", filepath.Join(dir, "a10r.log"),
	}, args...))
	err = root.ExecuteContext(t.Context())
	return out.String(), err
}

// TestSnapshotCmd_WritesOneFrameToStdout is the end-to-end contract:
// the frame lands on stdout at the requested size, and stdout carries
// nothing else (ADR 0045).
func TestSnapshotCmd_WritesOneFrameToStdout(t *testing.T) {
	stdout, err := runSnapshotCmd(t, "alerts", "--width", "100", "--height", "30")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.Len(t, lines, 30, "stdout must carry the frame and nothing else")
	for i, line := range lines {
		require.LessOrEqual(t, len([]rune(line)), 100, "line %d overflows the requested width", i)
	}
	require.Contains(t, stdout, "<alerts>", "the footer crumb belongs to the frame")
	require.NotContains(t, stdout, "\x1b[", "colour is off by default")
}

// TestSnapshotCmd_ColorKeepsEscapes: the screenshot pipeline is the
// only caller that wants the escapes, so the default stays plain.
func TestSnapshotCmd_ColorKeepsEscapes(t *testing.T) {
	stdout, err := runSnapshotCmd(t, "alerts", "--color")
	require.NoError(t, err)
	require.Contains(t, stdout, "\x1b[")
}

// TestSnapshotCmd_UnknownPageLeavesStdoutEmpty pins the failure
// shape: the error goes to the renderer, never a half-frame to
// stdout.
func TestSnapshotCmd_UnknownPageLeavesStdoutEmpty(t *testing.T) {
	stdout, err := runSnapshotCmd(t, "nope")
	require.ErrorIs(t, err, boot.ErrUnknownPage)
	require.Empty(t, stdout)
}

// TestSnapshotCmd_RequiresExactlyOnePage: no page and two pages are
// both a mistake, and neither may put half a frame on stdout.
func TestSnapshotCmd_RequiresExactlyOnePage(t *testing.T) {
	for _, args := range [][]string{{}, {"alerts", "silences"}} {
		t.Run(strings.Join(args, "+"), func(t *testing.T) {
			stdout, err := runSnapshotCmd(t, args...)
			require.Error(t, err)
			require.Empty(t, stdout)
		})
	}
}

// TestSnapshotCmd_FlagDefaults keeps the flag help and the renderer
// fallbacks reading from the same constants.
func TestSnapshotCmd_FlagDefaults(t *testing.T) {
	t.Parallel()
	var flags GlobalFlags
	cmd := newSnapshotCmd(&flags, runSnapshot)
	require.True(t, cmd.Hidden, "snapshot is a maintainer tool, not a user surface")

	f := cmd.Flags()
	require.Equal(t, strconv.Itoa(boot.DefaultSnapshotWidth), f.Lookup("width").DefValue)
	require.Equal(t, strconv.Itoa(boot.DefaultSnapshotHeight), f.Lookup("height").DefValue)
	require.Equal(t, "false", f.Lookup("color").DefValue)

	for _, page := range boot.SnapshotPages() {
		require.Contains(t, cmd.Long, page, "the help must list every accepted page")
	}
}

// TestSnapshotCmd_AbsentFromHelp pins that the hidden command stays
// out of `a10r --help` and out of the ungrouped bucket.
func TestSnapshotCmd_AbsentFromHelp(t *testing.T) {
	t.Parallel()
	root := buildHelpRoot(t)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())
	require.NotContains(t, buf.String(), "  "+snapshotUse+" ",
		"a hidden command must not appear as a help entry")
}

// TestSnapshotCmd_UsesTheDefaultWaitWhenUnset pins the one snapshot
// knob with no flag behind it. A wait the command cannot set is the
// renderer's default on every run, so the default is the whole
// contract.
func TestSnapshotCmd_UsesTheDefaultWaitWhenUnset(t *testing.T) {
	t.Parallel()
	var flags GlobalFlags
	cmd := newSnapshotCmd(&flags, runSnapshot)

	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	require.ElementsMatch(t, []string{"width", "height", "color"}, names,
		"a knob of any name over the wait would make boot.DefaultSnapshotWait stop applying")
}
