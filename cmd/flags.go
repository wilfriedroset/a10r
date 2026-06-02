// SPDX-License-Identifier: Apache-2.0

package cmd

import "github.com/wilfriedroset/a10r/internal/config"

// GlobalFlags is the type cobra binds persistent flags onto. Alias
// (not a parallel struct) so the binder and config.Resolve share one
// shape. Extend config.CLIFlags, never this side — converting the
// alias into a new struct would split the shape the resolver expects.
type GlobalFlags = config.CLIFlags
