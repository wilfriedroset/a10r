// SPDX-License-Identifier: Apache-2.0

// Package output renders read-only command results in one of three
// formats: table (TTY default), json (pipe default, jq-friendly),
// yaml (human-edit-friendly). Each command knows its own payload
// type — this package supplies generic encoders for json/yaml plus
// a tabwriter-backed Table helper, leaving the per-command
// row-flattening to the command itself.
//
// JSON output schema is documented in docs/end-users/output-
// formats.md, which carries the format-stability disclaimer.
// Field stability is not yet promised; structural shape (one
// top-level object per command, items keyed by name) is the
// implicit contract.
package output

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Format names the user-selectable output rendering. A zero
// Format is invalid; callers feed user input through ParseFormat
// to get a validated value.
type Format string

const (
	// FormatTable renders rows through a tabwriter for human
	// reading. Default when stdout is a TTY.
	FormatTable Format = "table"

	// FormatJSON marshals the payload via encoding/json with a
	// 2-space indent. Default when stdout is a pipe; the format
	// CI wrappers and jq pipelines target.
	FormatJSON Format = "json"

	// FormatYAML marshals the payload via gopkg.in/yaml.v3.
	// Useful for diffing and copy-paste into config snippets.
	FormatYAML Format = "yaml"
)

// ErrUnknownFormat is returned by ParseFormat when the supplied
// value is not one of the supported formats. Wrapped with the
// offending value and the accept-list so the user message is
// actionable.
var ErrUnknownFormat = errors.New("unknown output format")

// supportedFormats is the validation source for ParseFormat and
// drives the human-readable accept-list in its error messages.
// Single source of truth: adding a Format means appending here.
var supportedFormats = []Format{FormatTable, FormatJSON, FormatYAML}

// ParseFormat validates s against the supported set. Empty maps to a
// zero Format so the caller can layer TTY-vs-pipe default resolution
// after parsing — this function only enforces "if you supplied
// something, it must be valid".
func ParseFormat(s string) (Format, error) {
	if s == "" {
		return "", nil
	}
	f := Format(s)
	if slices.Contains(supportedFormats, f) {
		return f, nil
	}
	quoted := make([]string, len(supportedFormats))
	for i, sf := range supportedFormats {
		quoted[i] = fmt.Sprintf("%q", string(sf))
	}
	return "", fmt.Errorf("%w: %q (want one of %s)",
		ErrUnknownFormat, s, strings.Join(quoted, ", "))
}
