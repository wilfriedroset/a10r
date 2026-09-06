// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/wilfriedroset/a10r/internal/output"
)

// errTableUnsupported is returned when a detail (get) command is asked
// for --output=table. A single record has no row grid; the detail
// verbs render the full nested payload as json or yaml instead.
var errTableUnsupported = errors.New(
	"table output is not supported for get; use --output=json or --output=yaml",
)

// resolveDetailFormat picks the output format for a detail (get)
// command. Unlike the list commands, the TTY default is yaml (a record
// reads better as a document than a one-row table, and yaml round-trips
// into config / editor buffers); a pipe still defaults to json for
// downstream tooling. The accept-list is json/yaml only — explicit table
// is rejected, and an unknown value names just the formats this verb
// takes rather than the list commands' wider set. Matching is exact (no
// case-folding), as in the list commands, so the flag behaves uniformly.
func resolveDetailFormat(raw string, getenv func(string) string, tty bool) (output.Format, error) {
	switch raw {
	case "":
		return output.ResolveAgentAware("", getenv, tty,
			[]output.Format{output.FormatJSON, output.FormatYAML},
			output.FormatYAML, output.FormatJSON), nil
	case string(output.FormatJSON):
		return output.FormatJSON, nil
	case string(output.FormatYAML):
		return output.FormatYAML, nil
	case string(output.FormatTable):
		return "", errTableUnsupported
	default:
		return "", fmt.Errorf("unknown output format %q (want json or yaml)", raw)
	}
}

// renderDetail writes the matched records: a single document when
// exactly one matched (the editor-buffer-compatible shape, and what a
// fingerprint / id resolves to in the common single-tenant case), or a
// sequence when one identity resolved across several tenants.
func renderDetail[T any](out io.Writer, format output.Format, matches []T) error {
	var payload any = matches
	if len(matches) == 1 {
		payload = matches[0]
	}
	switch format {
	case output.FormatJSON:
		if err := output.WriteJSON(out, payload); err != nil {
			return fmt.Errorf("write json: %w", err)
		}
		return nil
	case output.FormatYAML:
		if err := output.WriteYAML(out, payload); err != nil {
			return fmt.Errorf("write yaml: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("render detail: unsupported format %q", format)
	}
}

// emitDetail is the shared tail of every get verb: print per-backend
// failures to errOut, flatten the matches in backend order, then either
// render them or map the empty result to an exit code. No match while
// every backend failed is ExitUnreachable ("couldn't look"); no match
// while at least one backend answered is ExitNotFound ("not there").
func emitDetail[T any](
	out, errOut io.Writer,
	results []backendResult[[]T],
	resource, identity string,
	format output.Format,
) error {
	failed, total := emitBackendErrors(errOut, results)

	var matches []T
	for _, r := range results {
		matches = append(matches, r.value...)
	}
	if len(matches) == 0 {
		if total > 0 && failed == total {
			return NewExitError(ExitUnreachable,
				fmt.Errorf("every backend in scope failed; %s %q not confirmed", resource, identity))
		}
		return NewExitError(ExitNotFound,
			fmt.Errorf("%s %q not found in scope", resource, identity))
	}
	return renderDetail(out, format, matches)
}
