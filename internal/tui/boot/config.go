// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/wilfriedroset/a10r/internal/config"
)

// loadConfigForTUI loads the user config; missing config returns a
// zero Config so the program still starts. errOut receives the
// one-line "no config found" hint so the operator sees the next step.
// found is false for that case, which the `:info` report prints as
// the same "config: not found" line `a10r info` does.
func loadConfigForTUI(flags *config.CLIFlags, load func(config.LoadOpts) (*config.Config, error), errOut io.Writer) (cfg *config.Config, found bool, err error) {
	cfg, err = load(LoadOptsFromFlags(flags))
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			fmt.Fprintln(errOut, "no config found — starting with empty backend list (run `a10r validate` after editing your config)")
			return &config.Config{}, false, nil
		}
		return nil, false, err
	}
	return cfg, true, nil
}

// startupConfig is the config a fresh session boots on. Unlike a
// reload, a missing file is tolerated, and the --tenant scope is
// checked against the resolved backends before anything is built.
func startupConfig(flags *config.CLIFlags, load func(config.LoadOpts) (*config.Config, error), errOut io.Writer) (eff config.Effective, found bool, err error) {
	cfg, found, err := loadConfigForTUI(flags, load, errOut)
	if err != nil {
		return config.Effective{}, false, err
	}
	eff, err = resolveEffectiveConfig(flags, cfg)
	if err != nil {
		return config.Effective{}, false, err
	}
	if err := config.ValidateScope(eff.Config.Backends, eff.Tenant); err != nil {
		return config.Effective{}, false, err //nolint:wrapcheck // the message already names the flag and the bad tenant.
	}
	return eff, found, nil
}

// LoadOptsFromFlags translates persistent flags into
// config.LoadOpts. --config (a file path) splits into Dir + File
// so the loader reads the requested file directly; --config-dir
// falls back to the XDG resolution path with the canonical
// "a10r.yaml" basename.
//
// Exported so every cmd-side read-only subcommand (alerts,
// silences, receivers, doctor, info, validate) agrees on the
// mapping.
func LoadOptsFromFlags(flags *config.CLIFlags) config.LoadOpts {
	if flags.ConfigPath != "" {
		return config.LoadOpts{
			Dir:  filepath.Dir(flags.ConfigPath),
			File: filepath.Base(flags.ConfigPath),
		}
	}
	return config.LoadOpts{Dir: flags.ConfigDir}
}
