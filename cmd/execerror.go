// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/output"
)

// execError is the structured envelope for a top-level failure, emitted on
// stderr when the caller selected a structured format (ADR 0045). stdout is
// left untouched so a `-o json` consumer never sees a half-data/half-error
// stream; Code mirrors the process exit code so it never disagrees with $?.
type execError struct {
	Error string `json:"error" yaml:"error"`
	Code  int    `json:"code" yaml:"code"`
}

// renderExecError reports a failed command's error on errOut. For a genuine
// top-level failure under a structured format it writes the {error,code}
// envelope; otherwise (human format, or a failure already emitted as data /
// the --fail signal) it writes the plain message, preserving the lines-mode
// behavior. getenv feeds A10R_OUTPUT and agent detection.
//
// The envelope is skipped when a structured result is already on stdout:
// write verbs (the result array) and doctor (its report) mark their exit
// errors Emitted, so the envelope would duplicate them. Read fan-outs that
// fail wholesale (exit 3/4) and gets that miss (exit 5) emit no stdout
// result that reports the failure (an all-failed list renders an empty
// array; a missed get renders nothing), so they ARE enveloped — their
// per-backend stderr lines are diagnostics, not a structured result.
func renderExecError(executed *cobra.Command, err error, getenv func(string) string, errOut io.Writer) {
	if err == nil {
		return
	}
	code := exitCodeFor(err)
	// An Emitted failure already carries its detail in the emitted output,
	// and --fail is a signal, not a failure: neither becomes an envelope.
	var ee *ExitError
	if (errors.As(err, &ee) && ee.Emitted) || code == ExitFailMatched {
		fmt.Fprintln(errOut, err)
		return
	}

	switch execErrorFormat(executed, getenv) {
	case output.FormatJSON:
		_ = output.WriteJSON(errOut, execError{Error: err.Error(), Code: code})
	case output.FormatYAML:
		_ = output.WriteYAML(errOut, execError{Error: err.Error(), Code: code})
	default:
		fmt.Fprintln(errOut, err)
	}
}

// execErrorFormat reports the structured format to envelope a failure in, or
// "" for the plain-message fallback. It keys off the failed command's bound
// --output flag plus A10R_OUTPUT and agent detection — never a tty/pipe data
// default, because an error envelope is opt-in via an explicit structured
// signal (flag, env, or detected agent), not the bare-pipe default.
func execErrorFormat(executed *cobra.Command, getenv func(string) string) output.Format {
	raw := ""
	if executed != nil {
		if f := executed.Flags().Lookup("output"); f != nil {
			raw = f.Value.String()
		}
	}
	// Defaults are "" so only an explicit structured signal (flag, env, or
	// agent) yields json/yaml; an explicit human flag (e.g. table) resolves
	// to itself and falls through to the plain-message default below.
	switch f := output.ResolveAgentAware(output.Format(raw), getenv, false,
		[]output.Format{output.FormatJSON, output.FormatYAML}, "", ""); f {
	case output.FormatJSON, output.FormatYAML:
		return f
	default:
		return ""
	}
}
