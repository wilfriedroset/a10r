// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
)

// silenceRecreateOptions bundles the recreate flags.
type silenceRecreateOptions struct {
	Ends      string
	Comment   string
	CreatedBy string
	Output    string
}

// newSilencesRecreateCmd is the headless complement to the TUI silence
// recreate (Ctrl+N from an expired silence). It derives a NEW silence
// from an existing one: the source's matchers and comment are copied,
// the start resets to now, and the author becomes the acting user. The
// source is read-only input — it is never modified, and may be in any
// lifecycle state (recreating an active silence just makes an
// overlapping one).
//
// --ends is required: recreate deliberately never reuses the source's
// window, because re-applying a stale window silently is the failure
// mode the verb exists to prevent. To change the matchers, use create
// instead — recreate is for the same target with a fresh window.
func newSilencesRecreateCmd(flags *GlobalFlags) *cobra.Command {
	var opts silenceRecreateOptions
	cmd := &cobra.Command{
		Use:   "recreate <id>",
		Short: "Create a new silence from an existing one (matchers and comment copied, window restated)",
		Args:  exactlyOneArg("a silence id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSilenceRecreate(cmd.Context(), cmd.OutOrStdout(), flags, args[0], opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Ends, "ends", "",
		"required new end: a duration (2h, 7d2h) from now, or an RFC3339 timestamp")
	f.StringVar(&opts.Comment, "comment", "", "override the copied comment")
	f.StringVar(&opts.CreatedBy, "created-by", "", "silence author (default: $USER, else a10r)")
	f.StringVarP(&opts.Output, "output", "o", "",
		"output format: default tab-separated tenant<TAB>id, or json, yaml")
	return cmd
}

func runSilenceRecreate(ctx context.Context, out io.Writer, flags *GlobalFlags, id string, opts silenceRecreateOptions) error {
	cfg, globalReadOnly, err := loadWriteConfig(flags)
	if err != nil {
		return err
	}
	build, closer, err := buildClientFactory(flags)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()

	creator := resolveCreator(opts.CreatedBy, os.Getenv("USER"))
	return silenceRecreate(ctx, out, os.Stderr, cfg, globalReadOnly, build, time.Now(), id, opts, creator)
}

// silenceRecreate resolves the source id, derives a fresh spec per found
// copy (matchers + comment carried, window and author restated), fails
// closed on read-only targets, then creates the new silences.
func silenceRecreate(
	ctx context.Context,
	out, errOut io.Writer,
	cfg *config.Config,
	globalReadOnly bool,
	build listcmd.ClientFactory,
	now time.Time,
	id string,
	opts silenceRecreateOptions,
	creator string,
) error {
	format, err := resolveWriteFormat(opts.Output)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.Ends) == "" {
		return errors.New("--ends is required: recreate never reuses the source's window (e.g. --ends 2h)")
	}
	end, err := parseSilenceEnd(opts.Ends, now)
	if err != nil {
		return fmt.Errorf("--ends: %w", err)
	}

	found, err := findSilences(ctx, errOut, cfg, build, id)
	if err != nil {
		return err
	}

	targets := make([]writeTarget, 0, len(found))
	for _, f := range found {
		comment := f.silence.Comment
		if opts.Comment != "" {
			comment = opts.Comment
		}
		if strings.TrimSpace(comment) == "" {
			return fmt.Errorf("silence %q in %q has no comment to copy; pass --comment", id, f.tenant)
		}
		targets = append(targets, writeTarget{tenant: f.tenant, spec: backend.SilenceSpec{
			Matchers:  f.silence.Matchers,
			StartsAt:  now,
			EndsAt:    end,
			CreatedBy: creator,
			Comment:   comment,
		}})
	}

	if err := ensureWritableTargets(globalReadOnly, cfg, targetTenants(targets)); err != nil {
		return err
	}
	return runWrites(ctx, out, errOut, cfg, build, format, "recreated", targets,
		func(ctx context.Context, c backend.Client, t writeTarget) (string, error) {
			return c.CreateSilence(ctx, t.spec)
		})
}
