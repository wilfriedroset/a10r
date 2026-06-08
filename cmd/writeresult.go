// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/output"
)

// writeResult is one backend's outcome for a silence write verb
// (create / update / expire / recreate). Status is the verb's
// past-tense success word ("created", "expired", ...) or "error";
// Error carries the failure message only when Status is "error".
type writeResult struct {
	Tenant string `json:"tenant" yaml:"tenant"`
	ID     string `json:"id" yaml:"id"`
	Status string `json:"status" yaml:"status"`
	Error  string `json:"error,omitempty" yaml:"error,omitempty"`
}

const (
	writeStatusError   = "error"
	writeStatusCreated = "created"

	// writeStatusPlanned marks a dry-run target the real run would write
	// cleanly (ADR 0046). It is a non-error status, so writeExitError
	// counts it as a success and a fully writable plan exits zero.
	writeStatusPlanned = "planned"

	// defaultCreator is the silence author used when neither --created-by
	// nor $USER is set, matching the TUI silence form's fallback.
	defaultCreator = "a10r"
)

// resolveWriteFormat picks the rendering mode for a write verb's result.
// With no --output flag the default is lines mode (tenant<TAB>id on
// stdout, narration on stderr — pipe-friendly), unless A10R_OUTPUT or an
// AI agent selects the structured json array instead (ADR 0045). Table
// is rejected: a write result is a stream of records, not a grid to page.
func resolveWriteFormat(raw string, getenv func(string) string) (output.Format, error) {
	switch raw {
	case "":
		return output.ResolveAgentAware("", getenv, false,
			[]output.Format{output.FormatJSON, output.FormatYAML}, "", ""), nil
	case string(output.FormatJSON):
		return output.FormatJSON, nil
	case string(output.FormatYAML):
		return output.FormatYAML, nil
	case string(output.FormatTable):
		return "", errors.New(
			"table output is not supported for write verbs; the default is tab-separated, or use --output=json or --output=yaml")
	default:
		return "", fmt.Errorf("unknown output format %q (want json or yaml)", raw)
	}
}

// resolveCreator applies the CreatedBy precedence: the explicit flag,
// then $USER, then the "a10r" fallback — matching the TUI silence form
// so a silence reads the same author whichever surface created it.
func resolveCreator(flagVal, userEnv string) string {
	if flagVal != "" {
		return flagVal
	}
	if userEnv != "" {
		return userEnv
	}
	return defaultCreator
}

// emitWriteResults renders the per-target outcomes and returns the exit
// error. In lines mode only successes print (tenant<TAB>id) to out, and
// failures print to errOut; json/yaml emit the whole array (successes
// and failures) to out. Any failure makes the command exit non-zero —
// write verbs are NOT lenient the way read fan-outs are, because a
// caller must learn that a silence it asked for did not land.
func emitWriteResults(out, errOut io.Writer, format output.Format, results []writeResult, opErrs []error) error {
	switch format {
	case output.FormatJSON:
		if err := output.WriteJSON(out, results); err != nil {
			return fmt.Errorf("write json: %w", err)
		}
	case output.FormatYAML:
		if err := output.WriteYAML(out, results); err != nil {
			return fmt.Errorf("write yaml: %w", err)
		}
	default:
		writeResultLines(out, errOut, results)
	}
	return writeExitError(results, opErrs)
}

// writeExitError maps the per-target outcomes to the command exit error.
// Any failure is non-zero (writes are not lenient). When every target
// failed the same transport class — all unreachable, or all auth — the
// typed code is surfaced so a CI wrapper can branch (retry vs fix creds),
// matching the read fan-out and the resolve phase of the other write
// verbs; a mixed or generic failure stays ExitRuntimeError.
func writeExitError(results []writeResult, opErrs []error) error {
	failed := countWriteFailures(results)
	if failed == 0 {
		return nil
	}
	msg := fmt.Errorf("%d of %d silence write(s) failed", failed, len(results))
	// The per-target outcomes were just emitted (a structured array on
	// stdout, or error lines on stderr), so these exit errors are marked
	// Emitted: the top-level renderer must not re-report them as an
	// envelope — the detail is already in the output (ADR 0045).
	//
	// runWrites keeps opErrs index-parallel to results; the length guard
	// only matters for the direct emitWriteResults test callers that pass
	// a nil opErrs, which then fall through to ExitRuntimeError.
	if failed == len(results) && len(opErrs) == len(results) {
		if allFailuresAre(opErrs, backend.ErrUnreachable) {
			return newEmittedError(ExitUnreachable, msg)
		}
		if allFailuresAre(opErrs, backend.ErrUnauthorized) {
			return newEmittedError(ExitAuthFailed, msg)
		}
	}
	return newEmittedError(ExitRuntimeError, msg)
}

// allFailuresAre reports whether every entry is a non-nil error matching
// target. Called only when every result failed, so a nil entry (a skip
// with no transport error) means the failures are not a uniform class.
func allFailuresAre(opErrs []error, target error) bool {
	if len(opErrs) == 0 {
		return false
	}
	for _, e := range opErrs {
		if e == nil || !errors.Is(e, target) {
			return false
		}
	}
	return true
}

// writeResultLines renders the default lines mode: a tenant<TAB>id line
// per success on out, and one failure line per error on errOut.
func writeResultLines(out, errOut io.Writer, results []writeResult) {
	for _, r := range results {
		if r.Status == writeStatusError {
			fmt.Fprintln(errOut, writeErrorLine(r))
			continue
		}
		fmt.Fprintf(out, "%s\t%s\n", r.Tenant, r.ID)
	}
}

// writeErrorLine labels a failure with its tenant, falling back to the
// id when there is no tenant (an id that resolved to no backend, e.g.
// expire of a missing id), and to the bare message if neither is set.
func writeErrorLine(r writeResult) string {
	switch {
	case r.Tenant != "":
		return fmt.Sprintf("%s: %s", r.Tenant, r.Error)
	case r.ID != "":
		return fmt.Sprintf("%s: %s", r.ID, r.Error)
	default:
		return r.Error
	}
}

// countWriteFailures tallies the error results, the count the exit error
// reports.
func countWriteFailures(results []writeResult) int {
	failed := 0
	for _, r := range results {
		if r.Status == writeStatusError {
			failed++
		}
	}
	return failed
}
