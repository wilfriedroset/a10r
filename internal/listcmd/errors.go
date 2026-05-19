// SPDX-License-Identifier: Apache-2.0

package listcmd

import "errors"

// ErrAllBackendsFailed is the canonical sentinel Pipeline.Run returns
// when every backend in the active scope failed to fetch its rows.
// The cmd layer matches via errors.Is and maps the sentinel onto
// ExitUnreachable so the package stays unaware of the exit-code
// table (ADR 0009 lives in cmd/exit.go).
var ErrAllBackendsFailed = errors.New("every configured backend failed to list")

// ErrMatched is the canonical sentinel Pipeline.Run returns when
// Spec.FailOnAny is set and at least one row survived the fetcher.
// Mirror of ErrAllBackendsFailed: the cmd layer maps it onto
// ExitFailMatched. Wrap with a count + ResourceLabel-derived message
// so the rendered error reads as today's
// "--fail: N alert(s) matched the filter".
var ErrMatched = errors.New("rows matched --fail filter")
