// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/wizard"
)

// newInitCmd returns the `a10r init` subcommand. Walks the user
// through a small set of prompts (backend kind, URL, auth, tenant,
// poll interval, theme) and writes the result to the resolved
// XDG config path.
//
// The `--force` flag overwrites an existing config file; without
// it the command refuses rather than silently clobbering the
// operator's hand-edited setup.
func newInitCmd(flags *GlobalFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Create a starter a10r.yaml via interactive prompts",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(initIO{
				In:    cmd.InOrStdin(),
				Out:   cmd.OutOrStdout(),
				Err:   cmd.ErrOrStderr(),
				Flags: flags,
				Force: force,
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"overwrite an existing a10r.yaml without prompting")
	return cmd
}

// initIO bundles the host-side handles runInit consumes so tests
// can inject a strings.Reader / bytes.Buffer pair without touching
// os.Stdin / os.Stdout.
type initIO struct {
	In    io.Reader
	Out   io.Writer
	Err   io.Writer
	Flags *GlobalFlags
	Force bool
}

// runInit drives the interactive flow and writes the resulting
// YAML to the resolved config path. Wrapped errors carry an
// ExitConfigInvalid code so a caller's CI pipeline can branch on
// "the wizard refused to write" without parsing stderr.
func runInit(env initIO) error {
	dir, err := config.ResolveDir(env.Flags.ConfigDir)
	if err != nil {
		return NewExitError(ExitConfigInvalid, fmt.Errorf("resolve config dir: %w", err))
	}
	path := filepath.Join(dir, "a10r.yaml")

	if !env.Force {
		if _, err := os.Stat(path); err == nil {
			return NewExitError(ExitConfigInvalid,
				fmt.Errorf("refusing to overwrite %s — pass --force to confirm", path))
		}
	}

	cfg, err := promptConfig(env.In, env.Out)
	if err != nil {
		return fmt.Errorf("init wizard: %w", err)
	}

	if err := writeInitConfig(path, cfg); err != nil {
		return NewExitError(ExitConfigInvalid, err)
	}

	fmt.Fprintf(env.Out, "wrote %s\n", path)
	return nil
}

// promptConfig walks the user through the prompt sequence and
// returns the resulting Config. Pure logic on top of wizard so
// tests can drive it directly with a strings.Reader fixture.
func promptConfig(in io.Reader, out io.Writer) (config.Config, error) {
	p := wizard.New(in, out)

	name, err := p.String("backend name", "prod", validateBackendName)
	if err != nil {
		return config.Config{}, err
	}
	urlStr, err := p.String("backend URL", "", validateURL)
	if err != nil {
		return config.Config{}, err
	}
	kind, err := p.Choice("backend kind", []string{"alertmanager", "mimir"}, "alertmanager")
	if err != nil {
		return config.Config{}, err
	}

	be := config.Backend{Name: name, URL: urlStr}
	if kind == "mimir" {
		tenant, err := p.String("tenant ID (X-Scope-OrgID)", "", nil)
		if err != nil {
			return config.Config{}, err
		}
		be.TenantHeader = "X-Scope-OrgID"
		be.Tenant = tenant
		be.Prefix = "/alertmanager"
	}

	authMode, err := p.Choice("authentication", []string{"none", "bearer", "basic"}, "none")
	if err != nil {
		return config.Config{}, err
	}
	switch authMode {
	case "bearer":
		token, err := p.String("bearer token", "", nonEmpty("token"))
		if err != nil {
			return config.Config{}, err
		}
		be.BearerToken = token
	case "basic":
		user, err := p.String("username", "", nonEmpty("username"))
		if err != nil {
			return config.Config{}, err
		}
		pass, err := p.String("password", "", nonEmpty("password"))
		if err != nil {
			return config.Config{}, err
		}
		be.BasicAuth = &config.BasicAuth{Username: user, Password: pass}
	}

	pollStr, err := p.String("default poll interval", "30s", validateDuration)
	if err != nil {
		return config.Config{}, err
	}
	pollInterval, _ := time.ParseDuration(pollStr) // already validated

	theme, err := p.Choice("theme",
		[]string{"catppuccin-mocha", "catppuccin-latte", "gruvbox-dark"},
		"catppuccin-mocha")
	if err != nil {
		return config.Config{}, err
	}

	return config.Config{
		Backends: []config.Backend{be},
		Defaults: config.Defaults{PollInterval: pollInterval},
		Theme:    config.Theme{Name: theme},
	}, nil
}

// writeInitConfig serialises cfg to path with 2-space indent
// (matching the file-side convention) and 0o600 permissions —
// the file may carry credentials, so it must not be world-
// readable. Creates intermediate directories.
func writeInitConfig(path string, cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = f.Close() }()
	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return enc.Close()
}

// validateBackendName rejects empty / whitespace / colliding
// names. Length cap matches the schema constraint.
func validateBackendName(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("name cannot be empty")
	}
	if len(s) > 64 {
		return errors.New("name too long (max 64 chars)")
	}
	return nil
}

// validateURL ensures the value parses as a URL with a scheme.
func validateURL(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("URL cannot be empty")
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return errors.New("URL must include scheme and host (e.g. https://am.example)")
	}
	return nil
}

// validateDuration accepts every value time.ParseDuration would
// accept.
func validateDuration(s string) error {
	if _, err := time.ParseDuration(s); err != nil {
		return fmt.Errorf("not a valid duration: %w", err)
	}
	return nil
}

// nonEmpty returns a validator that rejects empty / whitespace
// input with a field-named error message.
func nonEmpty(field string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s cannot be empty", field)
		}
		return nil
	}
}
