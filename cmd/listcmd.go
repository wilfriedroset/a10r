// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
)

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
