// SPDX-License-Identifier: Apache-2.0

package cmd

import "github.com/wilfriedroset/a10r/internal/config"

// GlobalFlags is the type cobra binds persistent flags onto.
//
// It is a type alias for config.CLIFlags rather than a parallel
// struct so the cobra binder (this package) and the precedence
// resolver (internal/config.Resolve) share one shape and conversion
// is a no-op. The struct definition lives in internal/config/resolve.go
// because the resolver is the canonical consumer; cmd's role is just
// to populate it via cobra.
//
// Do NOT extend this type by adding fields here — extend
// config.CLIFlags instead. Adding fields on the alias side is a
// compile error; this comment exists so a future contributor does
// not "fix" the error by converting the alias into a new struct.
type GlobalFlags = config.CLIFlags
