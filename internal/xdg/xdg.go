// SPDX-License-Identifier: Apache-2.0

// Package xdg holds the env-var slot names and Windows-fallback
// error shared by every a10r package that resolves an OS-conformant
// path (config dir, log file, future cache dir, …). One source of
// truth means a Windows fallback rename or a typo in the env-var
// name lands in one place.
package xdg

import "errors"

// ConfigHome is the env var consulted on Unix for the XDG-style
// config directory (ADR 0027's env-var slot in the resolution chain).
const ConfigHome = "XDG_CONFIG_HOME"

// StateHome is the env var consulted on Unix for the XDG-style state
// directory (logs and other rotating runtime data).
const StateHome = "XDG_STATE_HOME"

// LocalAppData is the env var consulted on Windows for the per-user
// roaming-disabled application data root.
const LocalAppData = "LOCALAPPDATA"

// ErrLocalAppDataMissing is returned by OS-path resolvers on Windows
// when %LOCALAPPDATA% is unset — there is no sensible fallback on
// Windows without it.
var ErrLocalAppDataMissing = errors.New("LOCALAPPDATA not set")
