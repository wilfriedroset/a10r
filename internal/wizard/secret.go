// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"
)

// errSecretCancelled is the package-private sentinel returned for
// both cancel triggers (Ctrl-C, Ctrl-D-on-empty) so the two paths
// share one error value. Not exported: callers only see the
// wrapped message ("read secret: cancelled") and treat any wizard
// error uniformly — see Prompter.Secret's cancellation doc.
var errSecretCancelled = errors.New("read secret: cancelled")

// processSecret reads a single line from in, echoing one `*` to out
// per printable byte and `\b \b` (cursor-back / space-over / cursor-
// back) for each backspace. Submit on `\r` or `\n`; Ctrl-C and
// Ctrl-D on an empty buffer cancel; arrow keys / CSI sequences /
// other control bytes are silently discarded so they don't pollute
// the buffer with `^[[A` garbage.
//
// Pure on its inputs — the raw-mode plumbing (MakeRaw / Restore)
// lives in readSecretFromTTY, which wraps this. Tests drive the
// engine with strings.Reader fixtures simulating keystrokes
// (`"se\x7fcret\r"`, `"abc\x03"`, …).
func processSecret(in io.Reader, out io.Writer) (string, error) {
	s := secretState{r: bufio.NewReader(in), out: out}
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("read secret: unexpected EOF")
			}
			return "", fmt.Errorf("read secret: %w", err)
		}
		if done, v, err := s.step(b); done {
			return v, err
		}
	}
}

// secretState bags the per-prompt mutable state: the input reader
// (so an escape sequence handler can consume follow-up bytes), the
// output writer (for the `*` / `\b \b` display), and the running
// byte buffer of accepted keystrokes. Methods mutate buf in place;
// the pointer-receiver shape keeps step() simple and the slice
// growth visible to the loop in processSecret.
type secretState struct {
	r   *bufio.Reader
	out io.Writer
	buf []byte
}

// step applies one keystroke byte. Returns done=true exactly when
// the loop should exit (submit or cancel). The per-byte switch
// lives here rather than inline in processSecret so each function
// stays under the project's cyclomatic-complexity bar.
func (s *secretState) step(b byte) (done bool, value string, err error) {
	switch b {
	case '\r', '\n':
		// raw mode: terminal driver doesn't translate CR→CRLF, so
		// we have to emit the linebreak ourselves to drop the
		// cursor onto the next row before the caller continues.
		fmt.Fprint(s.out, "\r\n")
		return true, string(s.buf), nil
	case 0x03: // Ctrl-C
		fmt.Fprint(s.out, "\r\n")
		return true, "", errSecretCancelled
	case 0x04: // Ctrl-D
		if len(s.buf) > 0 {
			// Non-empty buffer: Ctrl-D only acts as EOF on an
			// empty line — otherwise silently discard rather
			// than truncating mid-secret.
			return false, "", nil
		}
		fmt.Fprint(s.out, "\r\n")
		return true, "", errSecretCancelled
	case 0x7f, 0x08: // DEL / Backspace
		s.erase()
		return false, "", nil
	case 0x1b: // ESC — discard the rest of the CSI/SS3 sequence
		discardEscape(s.r)
		return false, "", nil
	}
	s.appendPrintable(b)
	return false, "", nil
}

// erase removes the most-recent byte from the buffer and emits
// the standard back-space-back erase trio so the matching `*`
// disappears from the user's view. No-op on an empty buffer.
func (s *secretState) erase() {
	if len(s.buf) == 0 {
		return
	}
	s.buf = s.buf[:len(s.buf)-1]
	fmt.Fprint(s.out, "\b \b")
}

// appendPrintable adds b to the buffer and echoes a `*` when b
// is a printable byte; other control bytes (Tab, Ctrl-<letter>
// not handled in step's switch) are silently dropped.
func (s *secretState) appendPrintable(b byte) {
	if b < 0x20 || b == 0x7f {
		return
	}
	s.buf = append(s.buf, b)
	fmt.Fprint(s.out, "*")
}

// discardEscape consumes the rest of an ANSI escape sequence after
// the leading `\x1b` has already been read. Handles CSI (`\x1b[…X`)
// and SS3 (`\x1b O X`) by reading until a "final byte" (`0x40`–
// `0x7E`); plain Alt+key (`\x1b X`) consumes one extra byte and
// stops. The 16-byte cap is a runaway guard for malformed input.
//
// Caveat: a bare Esc keypress (`\x1b` with no follow-up) will
// block on the next ReadByte until the user presses another key —
// real terminals don't deliver an Esc-alone keystroke without a
// timeout we don't implement here. Practical impact: zero —
// secret-prompt users have no reason to press Esc, and the
// EOF / Ctrl-C cancel paths are the documented way to abort.
func discardEscape(r *bufio.Reader) {
	b, err := r.ReadByte()
	if err != nil {
		return
	}
	if b != '[' && b != 'O' {
		// Alt+<key> or unknown introducer — one extra byte already
		// consumed, sequence ends here.
		return
	}
	for range 16 {
		next, err := r.ReadByte()
		if err != nil {
			return
		}
		// Final byte of a CSI / SS3 sequence is in 0x40–0x7E.
		if next >= 0x40 && next <= 0x7E {
			return
		}
	}
	// Fell off the 16-byte cap without finding a terminator — the
	// next ReadByte in processSecret will treat whatever follows
	// as a fresh keystroke, which may corrupt the user's next
	// character. In practice no real CSI sequence exceeds 16
	// bytes, so this branch only fires on malformed input.
}

// readSecretFromTTY is the raw-mode wrapper around processSecret.
// Switches the input fd into raw mode, defers the restore so a
// panic or early-return doesn't leave the terminal stuck, and runs
// the engine over the file pair. Only callable through the TTY
// constructor — non-TTY callers reach processSecret via the plain
// io.Reader path or skip it entirely for the line-buffered
// fallback.
func readSecretFromTTY(in, out *os.File) (string, error) {
	oldState, err := term.MakeRaw(in.Fd())
	if err != nil {
		return "", fmt.Errorf("enter raw mode: %w", err)
	}
	defer func() { _ = term.Restore(in.Fd(), oldState) }()
	return processSecret(in, out)
}
