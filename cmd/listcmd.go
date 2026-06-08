// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
	"github.com/wilfriedroset/a10r/internal/output"
)

// commonListFlags is the Output + FailOnAny pair every list
// subcommand exposes. Embedded into each per-command options struct
// so adding a new shared flag (or renaming one) lands in one place.
type commonListFlags struct {
	Output    string
	FailOnAny bool
}

// newListCmd builds the cobra skeleton shared by every
// `<resource> list` subcommand: the "list" verb, NoArgs, and the
// universal --output / --fail flags bound into common. Callers set
// RunE and add any resource-specific filter flags. failHelp differs
// per resource because what a matched row means for the exit-10 gate
// is resource-specific.
func newListCmd(short, failHelp string, common *commonListFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: short,
		Args:  cobra.NoArgs,
	}
	cmd.Flags().StringVarP(&common.Output, "output", "o", "",
		"output format: table, json, yaml; auto-JSON under an AI agent or A10R_OUTPUT")
	cmd.Flags().BoolVar(&common.FailOnAny, "fail", false, failHelp)
	return cmd
}

// runList is the shared wrapper around listcmd.Run for every list
// subcommand. The caller hands a Spec carrying the per-command bits;
// runList parses rawFormat and injects the cross-cutting seams (config,
// client factory + defer-close, pager, stderr) before mapping the
// pipeline's sentinels to exit codes.
func runList[R any](ctx context.Context, out io.Writer, flags *GlobalFlags, rawFormat string, spec listcmd.Spec[R]) error {
	format, err := output.ParseFormat(rawFormat)
	if err != nil {
		return fmt.Errorf("parse output format: %w", err)
	}
	cfg, err := loadCmdConfig(flags)
	if err != nil {
		return err
	}
	build, closer, err := buildClientFactory(flags)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()

	spec.Config = cfg
	spec.Format = format
	spec.Getenv = os.Getenv
	spec.NoPager = flags.NoPager
	spec.Out = out
	spec.Deps = listcmd.Deps{BuildClient: build, PagerFactory: newPagerWriteCloser, Stderr: os.Stderr}
	return mapPipelineExit(listcmd.Run(ctx, spec))
}

// loadCmdConfig is the listcmd-shared wrapper around config.Load
// that owns the ExitConfigInvalid mapping. Pipeline stays unaware
// of cmd's exit-code table; every list command routes through this
// helper so the mapping is set once.
//
// --tenant narrows cfg.Backends to the in-scope subset here so every
// headless command (reads and silence writes alike) shares one
// targeting story: a single tenant, a comma-list, or all. A scope
// naming no configured backend is a usage error, not a silent empty
// result — surfacing it stops a typo'd `--tenant prdo` from quietly
// becoming a no-op (or, for a write fan-out, hitting the wrong set).
func loadCmdConfig(flags *GlobalFlags) (*config.Config, error) {
	cfg, err := config.Load(loadOptsFromFlags(flags))
	if err != nil {
		return nil, NewExitError(ExitConfigInvalid, fmt.Errorf("load config: %w", err))
	}
	if err := applyTenantScope(cfg, flags.Tenant); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadWriteConfig is loadCmdConfig's sibling for the silence write
// verbs. It additionally runs config.Resolve so the effective read-only
// knob (--read-only / A10R_READ_ONLY / defaults.read_only) is honoured,
// returning it alongside the scoped config for the fail-closed gate.
// The read commands skip this because they never mutate, so the knob is
// irrelevant to them.
func loadWriteConfig(flags *GlobalFlags) (*config.Config, bool, error) {
	cfg, err := config.Load(loadOptsFromFlags(flags))
	if err != nil {
		return nil, false, NewExitError(ExitConfigInvalid, fmt.Errorf("load config: %w", err))
	}
	eff, err := config.Resolve(*flags, os.Getenv, *cfg)
	if err != nil {
		return nil, false, NewExitError(ExitConfigInvalid, fmt.Errorf("resolve config: %w", err))
	}
	resolved := eff.Config
	if err := applyTenantScope(&resolved, flags.Tenant); err != nil {
		return nil, false, err
	}
	// A write verb needs at least one backend to act on. An empty backend
	// set (a config with no backends — a non-empty --tenant that matches
	// nothing already errored in applyTenantScope) would otherwise fan out
	// to nothing and exit 0 silently, which reads as success.
	if len(resolved.Backends) == 0 {
		return nil, false, NewExitError(ExitConfigInvalid,
			errors.New("no backends configured; add a backend to a10r.yaml before writing silences"))
	}
	return &resolved, resolved.Defaults.ReadOnly, nil
}

// applyTenantScope narrows cfg.Backends to the --tenant subset in place.
// Every named element must match a configured backend: a scope like
// `prod,bogus` errors naming `bogus` rather than silently narrowing to
// prod, since a typo'd element would otherwise drop a tenant the operator
// believed was in scope. Validation is skipped for an empty config so the
// empty-config read path stays a no-op (the write path rejects an empty
// backend set separately in loadWriteConfig).
func applyTenantScope(cfg *config.Config, tenant string) error {
	if len(cfg.Backends) > 0 {
		if unknown := config.UnknownScopeTenants(cfg.Backends, tenant); len(unknown) > 0 {
			quoted := make([]string, len(unknown))
			for i, u := range unknown {
				quoted[i] = fmt.Sprintf("%q", u)
			}
			return fmt.Errorf("no configured backend matches --tenant %s", strings.Join(quoted, ", "))
		}
	}
	cfg.Backends = config.ScopeBackends(cfg.Backends, tenant)
	return nil
}

// buildClientFactory returns the listcmd.ClientFactory the pipeline
// invokes per backend, plus the closer that flushes the
// --debug-http logger on shutdown. Wiring lives here (not on each
// per-command Spec builder) so the four list commands stay
// symmetric and so a future change to the User-Agent or factory
// option set touches one site, not four.
func buildClientFactory(flags *GlobalFlags) (listcmd.ClientFactory, io.Closer, error) {
	debugLog, closer, err := buildHTTPDebugLogger(flags)
	if err != nil {
		return nil, noopCloser{}, err
	}
	ua := userAgent(version, commit)
	var opts []factory.Option
	if debugLog != nil {
		opts = append(opts, factory.WithDebugLog(debugLog))
	}
	return func(be config.Backend) (backend.Client, error) {
		return factory.Build(be, ua, opts...)
	}, closer, nil
}

// newPagerWriteCloser adapts cmd.NewPager to the listcmd.PagerFactory
// signature. cmd.NewPager returns the concrete *Pager (which already
// satisfies io.WriteCloser); the conversion is a one-liner so the
// pipeline keeps its zero-dependency-on-cmd guarantee.
func newPagerWriteCloser(ctx context.Context, fallback io.Writer, outIsTerminal, noPager bool) (io.WriteCloser, error) {
	return NewPager(ctx, fallback, outIsTerminal, noPager)
}

// mapPipelineExit translates listcmd's canonical sentinels into
// cmd's ExitError types. errors.Is keeps the seam loose (the
// pipeline wraps the sentinels with a templated message), so the
// error chain still surfaces the per-command count + label to the
// caller via err.Error().
func mapPipelineExit(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, listcmd.ErrMatched):
		return NewExitError(ExitFailMatched, err)
	case errors.Is(err, listcmd.ErrAllBackendsFailed):
		return NewExitError(ExitUnreachable, err)
	}
	return err
}
