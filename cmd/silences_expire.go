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
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
	"github.com/wilfriedroset/a10r/internal/output"
)

// newSilencesExpireCmd is the headless complement to the TUI silence
// expire. It takes one or more ids and expires each. There is no
// confirmation prompt — typing an explicit id IS the confirmation, and
// a TTY-only prompt would fork interactive and scripted behaviour. The
// TUI's modal guards a cursor/marks gesture where the target is
// implicit; the CLI has no such ambiguity.
//
// Resolution is lenient per id (unlike create/update, which abort the
// whole request): a missing id or an already-expired silence is a
// per-id failure that still lets the other ids expire, but the command
// exits non-zero so a typo or stale id is never silently swallowed.
// Read-only targets still fail closed: if any resolved silence lives in
// a read-only backend, nothing is expired.
func newSilencesExpireCmd(flags *GlobalFlags) *cobra.Command {
	var outputFormat string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "expire <id> [<id>...]",
		Short: "Expire one or more silences by id",
		Example: `  # Expire one or more silences by id
  a10r silences expire a1b2c3d4 e5f6a7b8

  # Preview what would be expired, without writing
  a10r silences expire a1b2c3d4 --dry-run`,
		Args: atLeastOneArg("silence id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSilenceExpire(cmd.Context(), cmd.OutOrStdout(), flags, args, outputFormat, dryRun)
		},
	}
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "",
		"output format: default tab-separated tenant<TAB>id, or json, yaml; auto-JSON under an AI agent or A10R_OUTPUT")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"resolve and print what would be written, without making any change")
	return cmd
}

func runSilenceExpire(ctx context.Context, out io.Writer, flags *GlobalFlags, ids []string, rawFormat string, dryRun bool) error {
	cfg, globalReadOnly, err := loadWriteConfig(flags)
	if err != nil {
		return err
	}
	build, closer, err := buildClientFactory(flags)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	format, err := resolveWriteFormat(rawFormat, os.Getenv)
	if err != nil {
		return err
	}
	return silenceExpire(ctx, out, os.Stderr, cfg, globalReadOnly, build, ids, format, dryRun)
}

// expireHit is one backend's match when resolving the requested ids: the
// tenant, the id, and the silence's current state (so an already-expired
// silence is skipped rather than re-expired).
type expireHit struct {
	tenant string
	id     string
	state  backend.SilenceState
}

// silenceExpire resolves the requested ids across the in-scope backends,
// turns each into a write target (with a skip for already-expired or
// unresolved ids), fails closed on read-only targets, then expires the
// rest.
func silenceExpire(
	ctx context.Context,
	out, errOut io.Writer,
	cfg *config.Config,
	globalReadOnly bool,
	build listcmd.ClientFactory,
	ids []string,
	format output.Format,
	dryRun bool,
) error {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	results := fanOutBackends(ctx, cfg, build,
		func(ctx context.Context, tenant string, c backend.Client) ([]expireHit, error) {
			silences, err := c.ListSilences(ctx, backend.SilenceFilter{})
			if err != nil {
				return nil, fmt.Errorf("list silences: %w", err)
			}
			var hits []expireHit
			for _, s := range silences {
				if want[s.ID] {
					hits = append(hits, expireHit{tenant: tenant, id: s.ID, state: s.State})
				}
			}
			return hits, nil
		})
	failed, _ := emitBackendErrors(errOut, results)

	targets, foundCount := expireTargets(ids, results, failed > 0)
	// No requested id resolved anywhere: this is the same not-found
	// situation get/update/recreate report, so use the same exit codes
	// rather than the lenient per-id exit-1 — ExitUnreachable when a
	// backend failed (could not confirm), ExitNotFound otherwise. A
	// partial result (some ids found) stays lenient and falls through to
	// the per-target reporting below.
	if foundCount == 0 {
		if failed > 0 {
			return NewExitError(ExitUnreachable,
				fmt.Errorf("a backend in scope failed; silence(s) %s not confirmed", strings.Join(ids, ", ")))
		}
		return NewExitError(ExitNotFound,
			fmt.Errorf("silence(s) %s not found in scope", strings.Join(ids, ", ")))
	}
	if dryRun {
		return runDryRun(out, errOut, cfg, format, "expire", targets, globalReadOnly)
	}
	if err := ensureWritableTargets(globalReadOnly, cfg, targetTenants(targets)); err != nil {
		return err
	}
	return runWrites(ctx, out, errOut, cfg, build, format, "expired", targets, expiredHint,
		func(ctx context.Context, c backend.Client, t writeTarget) (string, error) {
			if err := c.ExpireSilence(ctx, t.id); err != nil {
				return "", fmt.Errorf("expire silence: %w", err)
			}
			return t.id, nil
		})
}

// expireTargets turns the per-backend hits into write targets: one per
// matched (tenant, id), already-expired ones carrying a skip so they are
// reported without a re-expire. Every requested id that matched nowhere
// becomes a tenant-less skip target so it surfaces as a per-id failure.
// anyBackendFailed shades the not-found message toward "could not
// confirm" so a miss behind an unreachable backend is not reported as a
// confident absence.
// The second return is the count of distinct requested ids that
// resolved to at least one backend, which the caller uses to tell a
// total miss (all not-found) from a partial one.
func expireTargets(ids []string, results []backendResult[[]expireHit], anyBackendFailed bool) (targets []writeTarget, foundCount int) {
	found := make(map[string]bool, len(ids))
	for _, r := range results {
		for _, h := range r.value {
			found[h.id] = true
			t := writeTarget{tenant: h.tenant, id: h.id}
			if h.state == backend.SilenceStateExpired {
				t.skip = errors.New("already expired")
			}
			targets = append(targets, t)
		}
	}
	for _, id := range ids {
		if found[id] {
			continue
		}
		reason := "not found in scope"
		if anyBackendFailed {
			reason = "not confirmed (a backend in scope failed)"
		}
		targets = append(targets, writeTarget{id: id, skip: errors.New(reason)})
	}
	return targets, len(found)
}
