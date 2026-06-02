// SPDX-License-Identifier: Apache-2.0

package listcmd

import "errors"

// ErrAllBackendsFailed is the sentinel Run returns when every backend failed;
// cmd/ matches it via errors.Is and maps onto ExitUnreachable (ADR 0009).
var ErrAllBackendsFailed = errors.New("every configured backend failed to list")

// ErrMatched is the sentinel Run returns when Spec.FailOnAny is set and rows
// survived; cmd/ maps it onto ExitFailMatched.
var ErrMatched = errors.New("rows matched --fail filter")
