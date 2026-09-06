// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// pagerFromEnv returns the pager command split into prog (program
// name) + args (extra flags). Resolution order:
//
//  1. $PAGER, when set and non-empty (split on whitespace —
//     standard behaviour matching git's `core.pager` and what
//     less / more / bat expect when the env var carries flags).
//  2. less -FRX — the conventional fallback. -F quits if the
//     output fits on one screen (so short tables don't trap the
//     operator in less); -R passes ANSI colour through; -X
//     suppresses the alt-screen so the rendered output stays
//     visible after the pager exits (k9s behaviour).
//
// Returns ("", nil) when no pager program could be located on
// PATH. Callers fall back to writing directly to stdout.
func pagerFromEnv() (prog string, args []string) {
	if env := os.Getenv("PAGER"); strings.TrimSpace(env) != "" {
		fields := strings.Fields(env)
		if _, err := exec.LookPath(fields[0]); err == nil {
			return fields[0], fields[1:]
		}
	}
	if _, err := exec.LookPath("less"); err == nil {
		return "less", []string{"-FRX"}
	}
	return "", nil
}

// Pager is a writer that pipes output through the resolved pager
// subprocess. Constructed via NewPager (live pager) or Disabled
// (write-through). Callers Write like any io.Writer; Close flushes
// the pipe, waits for the pager, and returns any pager error.
type Pager struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	target io.Writer
}

func (p *Pager) Write(b []byte) (int, error) {
	n, err := p.target.Write(b)
	if err != nil {
		return n, fmt.Errorf("write to pager: %w", err)
	}
	return n, nil
}

// Close flushes the pager's stdin and waits for the subprocess
// to exit. Idempotent: calling Close on a Disabled Pager is a
// no-op. Errors closing the stdin pipe are joined with the
// pager's exit error so the caller sees both.
func (p *Pager) Close() error {
	if p.cmd == nil {
		return nil
	}
	closeErr := p.stdin.Close()
	waitErr := p.cmd.Wait()
	// Less / more exit non-zero when the user pressed `q` mid-page;
	// that is a normal interactive exit, not a runtime failure.
	// Treat *exec.ExitError silently and surface only true I/O
	// problems (broken pipe, interrupted signal).
	if _, ok := errors.AsType[*exec.ExitError](waitErr); ok {
		waitErr = nil
	}
	return errors.Join(closeErr, waitErr)
}

// Disabled returns a write-through Pager for the paths where
// paging is not engaged (non-TTY, non-table, --no-pager) so call
// sites stay unconditional.
func Disabled(fallback io.Writer) *Pager {
	return &Pager{target: fallback}
}

// NewPager returns a paging Pager when (a) the resolved pager
// program is on PATH, (b) outIsTerminal reports true (the
// fallback writer is a TTY), and (c) noPager is false. Otherwise
// returns Disabled(fallback) so the caller can always wrap
// without conditional logic.
//
// Spawns the pager subprocess immediately and connects its
// stdout / stderr to the fallback (so error messages from the
// pager itself land where the operator expects). The supplied
// ctx propagates cancellation: a Ctrl-C on the parent ends the
// pager subprocess cleanly via SIGKILL — `less` left running
// after the parent exits is a UX trap.
func NewPager(ctx context.Context, fallback io.Writer, outIsTerminal, noPager bool) (*Pager, error) {
	if noPager || !outIsTerminal {
		return Disabled(fallback), nil
	}
	prog, args := pagerFromEnv()
	if prog == "" {
		return Disabled(fallback), nil
	}
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Stdout = fallback
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pager: stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pager: start %s: %w", prog, err)
	}
	return &Pager{cmd: cmd, stdin: stdin, target: stdin}, nil
}
