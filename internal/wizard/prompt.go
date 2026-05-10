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
package wizard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Prompter wraps an io.Reader (the user's stdin) and an io.Writer
// (the user's stdout) so tests can drive the helpers with
// strings.Reader / bytes.Buffer fakes. Construct via New.
type Prompter struct {
	in  *bufio.Scanner
	out io.Writer
}

// New constructs a Prompter over r and w. The reader is wrapped
// in a bufio.Scanner with the default line-by-line split, which
// matches every shell's interactive expectation.
func New(r io.Reader, w io.Writer) *Prompter {
	return &Prompter{in: bufio.NewScanner(r), out: w}
}

// String prompts for a free-form line of input. The defaultValue
// is shown in brackets and returned when the user just hits
// Enter. validate, when non-nil, is run on the resolved value;
// a non-nil return re-prompts. Returns an error only when the
// underlying reader fails (EOF on a closed pipe).
func (p *Prompter) String(question, defaultValue string, validate func(string) error) (string, error) {
	for {
		if defaultValue != "" {
			fmt.Fprintf(p.out, "%s [%s]: ", question, defaultValue)
		} else {
			fmt.Fprintf(p.out, "%s: ", question)
		}
		if !p.in.Scan() {
			if err := p.in.Err(); err != nil {
				return "", fmt.Errorf("read prompt: %w", err)
			}
			return "", errors.New("read prompt: unexpected EOF")
		}
		v := strings.TrimSpace(p.in.Text())
		if v == "" {
			v = defaultValue
		}
		if validate != nil {
			if err := validate(v); err != nil {
				fmt.Fprintf(p.out, "  invalid: %s\n", err)
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
		fmt.Fprintf(p.out, "%s (%s) [%s]: ", question, strings.Join(choices, "/"), defaultValue)
		if !p.in.Scan() {
			if err := p.in.Err(); err != nil {
				return "", fmt.Errorf("read prompt: %w", err)
			}
			return "", errors.New("read prompt: unexpected EOF")
		}
		v := strings.TrimSpace(p.in.Text())
		if v == "" {
			v = defaultValue
		}
		if slices.Contains(choices, v) {
			return v, nil
		}
		fmt.Fprintf(p.out, "  invalid: %q is not one of %s\n", v, strings.Join(choices, ", "))
	}
}

// Bool prompts the user for a yes/no answer. Default is shown as
// the capitalised choice (e.g. "[Y/n]" when defaultValue=true).
// Recognises `y`, `yes`, `n`, `no` case-insensitively; unknown
// input re-prompts.
func (p *Prompter) Bool(question string, defaultValue bool) (bool, error) {
	hint := "[y/N]"
	if defaultValue {
		hint = "[Y/n]"
	}
	for {
		fmt.Fprintf(p.out, "%s %s: ", question, hint)
		if !p.in.Scan() {
			if err := p.in.Err(); err != nil {
				return false, fmt.Errorf("read prompt: %w", err)
			}
			return false, errors.New("read prompt: unexpected EOF")
		}
		v := strings.ToLower(strings.TrimSpace(p.in.Text()))
		if v == "" {
			return defaultValue, nil
		}
		switch v {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintf(p.out, "  invalid: %q (want yes / no)\n", v)
	}
}
