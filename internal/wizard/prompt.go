// SPDX-License-Identifier: Apache-2.0

// Package wizard hosts a tiny interactive-prompt helper used by
// `a10r init` (and any future first-run flow that needs to gather
// values from a TTY without spinning up a bubbletea program).
//
// The helpers are deliberately stdin-line-oriented rather than
// bubbletea-driven: init runs before the TUI exists, the prompts
// are sequential, and a line-buffered shape is friendlier to
// scripted invocations (`echo prod | a10r init`) and tests
// (table-driven via a strings.Reader).
//
// Two public entry points:
//
//   - New(r, w) takes any io.Reader/io.Writer pair — used by tests
//     and any caller that explicitly wants the plain line-buffered
//     path with no color, no raw-mode redaction.
//   - From(in, out) auto-detects whether the handles are real
//     *os.File terminals and routes accordingly: TTY-wired pairs
//     get ANSI styling (honouring NO_COLOR and TERM=dumb) and the
//     raw-mode redacted-echo path for Secret(); non-TTY pairs
//     (pipes, tests' bytes.Buffer / strings.Reader) degrade to
//     the same shape as New — the prompter can't put a pipe into
//     raw mode anyway.
package wizard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"
)

// Prompter wraps the input/output handles and the rendering
// styler. Construct via New (plain) or NewTTY (raw-mode-capable).
type Prompter struct {
	in      *bufio.Scanner
	out     io.Writer
	styler  styler
	inFile  *os.File // set by newTTY only when stdin is a TTY — gates the raw-mode Secret path via tty()
	outFile *os.File // set unconditionally by newTTY — paired with inFile for the raw-mode call
}

// New constructs a plain Prompter over r and w. Color is forced
// off and Secret falls back to a line-buffered, unmasked read —
// the constructor's job is to keep tests (which pass a
// strings.Reader + bytes.Buffer) on a deterministic path with no
// TTY probing.
func New(r io.Reader, w io.Writer) *Prompter {
	return &Prompter{
		in:     bufio.NewScanner(r),
		out:    w,
		styler: newStyler(false),
	}
}

// From picks between the TTY-wired prompter (real terminal, raw-
// mode secret echo + ANSI styling) and the plain prompter (line
// path, no color) by type-asserting the in/out handles. Cobra
// hands the runtime entry real *os.File handles; tests pass
// strings.Reader / bytes.Buffer fakes — those fail the type
// assert and route to the plain prompter, which is why every
// prompt_test.go expectation keeps holding without a t.Setenv.
func From(in io.Reader, out io.Writer) *Prompter {
	inFile, inOK := in.(*os.File)
	outFile, outOK := out.(*os.File)
	if inOK && outOK {
		return newTTY(inFile, outFile)
	}
	return New(in, out)
}

// newTTY constructs a Prompter wired to a real terminal pair.
// Color is enabled by enableColor (TTY + NO_COLOR + TERM probes);
// Secret routes through the raw-mode redacted-echo path when
// stdin is a TTY. If either side is redirected (pipe, file), the
// corresponding feature degrades gracefully to the line-buffered,
// color-off equivalent. Package-private — callers reach it via
// From which makes the constructor choice from io.* handles.
func newTTY(stdin, stdout *os.File) *Prompter {
	p := &Prompter{
		in:      bufio.NewScanner(stdin),
		out:     stdout,
		styler:  newStyler(enableColor(stdout)),
		outFile: stdout,
	}
	if term.IsTerminal(stdin.Fd()) {
		p.inFile = stdin
	}
	return p
}

// enableColor probes whether ANSI styling should be emitted on
// stdout: stdout must be a TTY, NO_COLOR must be unset (any non-
// empty value disables — https://no-color.org), and TERM must not
// be "dumb".
func enableColor(stdout *os.File) bool {
	return term.IsTerminal(stdout.Fd()) &&
		os.Getenv("NO_COLOR") == "" &&
		os.Getenv("TERM") != "dumb"
}

// String prompts for a free-form line of input. The defaultValue
// is shown in brackets and returned when the user just hits
// Enter. validate, when non-nil, is run on the resolved value;
// a non-nil return re-prompts. Returns an error only when the
// underlying reader fails (EOF on a closed pipe).
func (p *Prompter) String(question, defaultValue string, validate func(string) error) (string, error) {
	for {
		fmt.Fprint(p.out, p.styler.String(question, defaultValue))
		v, err := p.readLine()
		if err != nil {
			return "", err
		}
		if v == "" {
			v = defaultValue
		}
		if validate != nil {
			if err := validate(v); err != nil {
				fmt.Fprint(p.out, p.styler.Invalid(err.Error()))
				continue
			}
		}
		return v, nil
	}
}

// Choice prompts the user to pick one entry from choices. Default
// value (one of choices) is shown in brackets and returned on
// empty input. Unrecognised input re-prompts with the choice list.
func (p *Prompter) Choice(question string, choices []string, defaultValue string) (string, error) {
	for {
		fmt.Fprint(p.out, p.styler.Choice(question, choices, defaultValue))
		v, err := p.readLine()
		if err != nil {
			return "", err
		}
		if v == "" {
			v = defaultValue
		}
		if slices.Contains(choices, v) {
			return v, nil
		}
		fmt.Fprint(p.out, p.styler.Invalid(
			fmt.Sprintf("%q is not one of %s", v, strings.Join(choices, ", "))))
	}
}

// Bool prompts the user for a yes/no answer. Default is shown as
// the capitalised choice (e.g. "[Y/n]" when defaultValue=true).
// Recognises `y`, `yes`, `n`, `no` case-insensitively; unknown
// input re-prompts.
func (p *Prompter) Bool(question string, defaultValue bool) (bool, error) {
	for {
		fmt.Fprint(p.out, p.styler.Bool(question, defaultValue))
		raw, err := p.readLine()
		if err != nil {
			return false, err
		}
		v := strings.ToLower(raw)
		if v == "" {
			return defaultValue, nil
		}
		switch v {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprint(p.out, p.styler.Invalid(
			fmt.Sprintf("%q (want yes / no)", v)))
	}
}

// readLine pulls one trimmed line from the underlying scanner.
// Centralised so String/Choice/Bool share the same EOF / read-
// error handling without duplicating the `if !p.in.Scan() { … }`
// dance three times.
func (p *Prompter) readLine() (string, error) {
	if !p.in.Scan() {
		if err := p.in.Err(); err != nil {
			return "", fmt.Errorf("read prompt: %w", err)
		}
		return "", errors.New("read prompt: unexpected EOF")
	}
	return strings.TrimSpace(p.in.Text()), nil
}

// Secret prompts for a sensitive value (password, bearer token).
// On a TTY the user's keystrokes are redacted with `*` per byte
// via the raw-mode engine; off-TTY (piped stdin, tests) falls
// back to a plain line read with no echo control — the prompter
// can't mask a pipe. Re-prompts on empty input.
//
// Cancellation: Ctrl-C or Ctrl-D-on-empty in raw-mode surfaces as
// an error whose message contains "cancelled"; the caller is
// expected to bubble it up so cobra prints the wizard-aborted
// line on stderr and exits non-zero. This is intentionally
// symmetric with how a stdin-EOF abort of String/Choice/Bool
// surfaces — every wizard-abort path terminates with an error,
// never a silent success.
func (p *Prompter) Secret(question string) (string, error) {
	for {
		fmt.Fprint(p.out, p.styler.String(question, ""))
		v, err := p.readSecret()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(v) == "" {
			fmt.Fprint(p.out, p.styler.Invalid("cannot be empty"))
			continue
		}
		return v, nil
	}
}

// tty reports whether the prompter is wired to a real terminal
// stdin (and therefore the raw-mode secret echo path is
// available). Set once at construction by newTTY — the From /
// New entry points keep p.inFile nil unless the underlying file
// reported as a TTY.
func (p *Prompter) tty() bool { return p.inFile != nil }

// readSecret picks the right reader based on whether the prompter
// is wired to a real TTY stdin. On a TTY → raw-mode redacted echo
// via readSecretFromTTY; otherwise → reuse the plain-line helper
// so the EOF / read-error wrapping stays consistent. The plain
// branch surfaces "read prompt: …" rather than "read secret: …"
// when stdin is piped — acceptable: a piped stdin closing mid-
// wizard is the same failure mode whether the prompt was secret
// or not.
func (p *Prompter) readSecret() (string, error) {
	if p.tty() {
		return readSecretFromTTY(p.inFile, p.outFile)
	}
	return p.readLine()
}
