// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

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
	cmd.Flags().StringVar(&common.Output, "output", "", "output format: table, json, yaml")
	cmd.Flags().BoolVar(&common.FailOnAny, "fail", false, failHelp)
	return cmd
}

// listRecipe is the per-command shape runListRecipe lifts into a
// listcmd.Spec. Holds the bits each subcommand customises while the
// helper bolts on the cross-cutting wiring (config load, factory,
// pager, stderr, mapPipelineExit).
type listRecipe[R any] struct {
	Format        string
	Fetcher       listcmd.Fetcher[R]
	Renderers     map[output.Format]listcmd.Renderer[R]
	Sort          func([]R)
	ResourceLabel string
	FailOnAny     bool
}

// runListRecipe is the shared wrapper around listcmd.Run for every
// list subcommand. Owns the cross-cutting scaffolding (ParseFormat,
// loadCmdConfig, buildClientFactory + defer-close, Spec assembly,
// mapPipelineExit) so each command just hands the per-command bits
// in via listRecipe.
func runListRecipe[R any](ctx context.Context, out io.Writer, flags *GlobalFlags, r listRecipe[R]) error {
	format, err := output.ParseFormat(r.Format)
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

	spec := listcmd.Spec[R]{
		Config:        cfg,
		Format:        format,
		Fetcher:       r.Fetcher,
		Renderers:     r.Renderers,
		Sort:          r.Sort,
		ResourceLabel: r.ResourceLabel,
		FailOnAny:     r.FailOnAny,
		NoPager:       flags.NoPager,
		Out:           out,
		Deps:          listcmd.Deps{BuildClient: build, PagerFactory: newPagerWriteCloser, Stderr: os.Stderr},
	}
	return mapPipelineExit(listcmd.Run(ctx, spec))
}

// loadCmdConfig is the listcmd-shared wrapper around config.Load
// that owns the ExitConfigInvalid mapping. Pipeline stays unaware
// of cmd's exit-code table; every list command routes through this
// helper so the mapping is set once.
func loadCmdConfig(flags *GlobalFlags) (*config.Config, error) {
	cfg, err := config.Load(loadOptsFromFlags(flags))
	if err != nil {
		return nil, NewExitError(ExitConfigInvalid, fmt.Errorf("load config: %w", err))
	}
	return cfg, nil
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
