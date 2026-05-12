// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// processSecret is the pure engine behind the raw-mode Secret
// prompt. It consumes a byte stream simulating keystrokes and
// writes display bytes (`*` per char, `\b \b` per backspace) to
// `out`. These table-driven tests pin every entry in the keystroke
// table from the design grilling, so a future refactor can't drift
// from the agreed behaviour without flipping a test red.

func TestProcessSecret_HappyPathReturnsTypedValue(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	got, err := processSecret(strings.NewReader("secret\r"), &out)
	require.NoError(t, err)
	require.Equal(t, "secret", got)
	// six chars typed → six stars echoed, then \r\n to break the
	// raw-mode line (terminal driver doesn't translate \r → \r\n in
	// raw mode, so the prompt code has to emit both bytes itself).
	require.Equal(t, "******\r\n", out.String())
}

func TestProcessSecret_LineFeedAlsoSubmits(t *testing.T) {
	t.Parallel()

	got, err := processSecret(strings.NewReader("abc\n"), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "abc", got)
}

func TestProcessSecret_BackspaceErasesLastByte(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	got, err := processSecret(strings.NewReader("se\x7fcret\r"), &out)
	require.NoError(t, err)
	require.Equal(t, "scret", got)
	// Byte stream: `*` `*` `\b \b` `*` `*` `*` `*` then `\r\n`.
	// The backspace emits the on-screen erase sequence but does
	// not strip earlier `*` bytes from the captured stream — the
	// stream is a record of what was sent to the terminal, not the
	// visual residue.
	require.Equal(t, "**\b \b****\r\n", out.String())
}

func TestProcessSecret_BackspaceOnEmptyBufferIsNoop(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	got, err := processSecret(strings.NewReader("\x7fabc\r"), &out)
	require.NoError(t, err)
	require.Equal(t, "abc", got)
	// no `\b \b` for the no-op backspace, then three stars.
	require.Equal(t, "***\r\n", out.String())
}

func TestProcessSecret_BackspaceAlt0x08AlsoErases(t *testing.T) {
	t.Parallel()

	got, err := processSecret(strings.NewReader("ab\x08c\r"), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "ac", got)
}

func TestProcessSecret_CtrlCCancels(t *testing.T) {
	t.Parallel()

	got, err := processSecret(strings.NewReader("abc\x03"), &bytes.Buffer{})
	require.Error(t, err)
	require.Empty(t, got)
	require.Contains(t, err.Error(), "cancelled")
}

func TestProcessSecret_CtrlDOnEmptyBufferCancels(t *testing.T) {
	t.Parallel()

	got, err := processSecret(strings.NewReader("\x04"), &bytes.Buffer{})
	require.Error(t, err)
	require.Empty(t, got)
}

func TestProcessSecret_CtrlDAfterCharsIsDiscarded(t *testing.T) {
	t.Parallel()

	got, err := processSecret(strings.NewReader("abc\x04def\r"), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "abcdef", got)
}

func TestProcessSecret_ArrowKeyEscapeSequenceIsDiscarded(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	// `ab` then up-arrow `\x1b[A` then `cd` then submit
	got, err := processSecret(strings.NewReader("ab\x1b[Acd\r"), &out)
	require.NoError(t, err)
	require.Equal(t, "abcd", got)
	// four stars only — the three escape bytes must be eaten silently
	require.Equal(t, "****\r\n", out.String())
}

func TestProcessSecret_MultiCharCSIIsFullyDiscarded(t *testing.T) {
	t.Parallel()

	// PgUp on xterm = \x1b[5~ (4 bytes). All four must be eaten.
	got, err := processSecret(strings.NewReader("a\x1b[5~b\r"), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "ab", got)
}

func TestProcessSecret_TabIsDiscarded(t *testing.T) {
	t.Parallel()

	got, err := processSecret(strings.NewReader("a\tb\r"), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "ab", got)
}

func TestProcessSecret_EOFBeforeEnterErrors(t *testing.T) {
	t.Parallel()

	got, err := processSecret(strings.NewReader("abc"), &bytes.Buffer{})
	require.Error(t, err)
	require.Empty(t, got)
	require.Contains(t, err.Error(), "EOF")
}

func TestProcessSecret_EmptySubmitReturnsEmptyString(t *testing.T) {
	t.Parallel()

	// Enter with no chars typed — engine returns ""; the Secret
	// caller is responsible for the "cannot be empty" re-prompt.
	got, err := processSecret(strings.NewReader("\r"), &bytes.Buffer{})
	require.NoError(t, err)
	require.Empty(t, got)
}
