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
	"github.com/wilfriedroset/a10r/internal/output"
)

// silenceUpdateOptions bundles the update flags. Every field is an
// override: an unset flag keeps the existing silence's value, so a lone
// --ends is the "extend this silence" one-liner.
type silenceUpdateOptions struct {
	Matchers  []string
	Starts    string
	Ends      string
	Comment   string
	CreatedBy string
	Output    string
	DryRun    bool
}

// hasMutation reports whether any override was supplied. An update with
// nothing to change is a usage error, not a no-op success.
func (o silenceUpdateOptions) hasMutation() bool {
	return len(o.Matchers) > 0 || o.Starts != "" || o.Ends != "" || o.Comment != "" || o.CreatedBy != ""
}

// newSilencesUpdateCmd is the headless complement to the TUI silence
// edit. It patches a silence in place: fetch the current spec, apply
// only the supplied flags, and submit the merged result. Repeatable
// --matcher replaces the whole matcher set (a set has no unambiguous
// per-element removal). Alertmanager keeps a silence's id across an
// update, so the id is stable.
//
// Only active or pending silences can be updated; an expired silence is
// immutable, so update points the operator at `silences recreate`
// instead of round-tripping a backend rejection.
func newSilencesUpdateCmd(flags *GlobalFlags) *cobra.Command {
	var opts silenceUpdateOptions
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Patch an existing silence in place (active or pending only)",
		Example: `  # Extend a silence's window, keeping everything else
  a10r silences update a1b2c3d4 --ends 8h

  # Preview the merged result without writing
  a10r silences update a1b2c3d4 --comment "extended for incident" --dry-run`,
		Args: exactlyOneArg("a silence id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSilenceUpdate(cmd.Context(), cmd.OutOrStdout(), flags, args[0], opts)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&opts.Matchers, "matcher", nil,
		"replacement matcher in Prometheus syntax (repeatable); replaces the whole set")
	f.StringVar(&opts.Starts, "starts", "", "new start: now or an RFC3339 timestamp")
	f.StringVar(&opts.Ends, "ends", "", "new end: a duration (2h, 7d2h) added to the start, or an RFC3339 timestamp")
	f.StringVar(&opts.Comment, "comment", "", "new comment")
	f.StringVar(&opts.CreatedBy, "created-by", "", "new author")
	f.StringVarP(&opts.Output, "output", "o", "",
		"output format: default tab-separated tenant<TAB>id, or json, yaml; auto-JSON under an AI agent or A10R_OUTPUT")
	f.BoolVar(&opts.DryRun, "dry-run", false,
		"resolve and print what would be written, without making any change")
	return cmd
}

func runSilenceUpdate(ctx context.Context, out io.Writer, flags *GlobalFlags, id string, opts silenceUpdateOptions) error {
	cfg, globalReadOnly, err := loadWriteConfig(flags)
	if err != nil {
		return err
	}
	build, closer, err := buildClientFactory(flags)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	format, err := resolveWriteFormat(opts.Output, os.Getenv)
	if err != nil {
		return err
	}
	return silenceUpdate(ctx, out, os.Stderr, cfg, globalReadOnly, build, time.Now(), id, opts, format)
}

// silenceUpdate resolves the id, merges the overrides onto each found
// silence, fails closed on read-only targets, then patches in place.
func silenceUpdate(
	ctx context.Context,
	out, errOut io.Writer,
	cfg *config.Config,
	globalReadOnly bool,
	build listcmd.ClientFactory,
	now time.Time,
	id string,
	opts silenceUpdateOptions,
	format output.Format,
) error {
	if !opts.hasMutation() {
		return errors.New(
			"nothing to update: pass at least one of --matcher, --starts, --ends, --comment, --created-by",
		)
	}

	found, err := findSilences(ctx, errOut, cfg, build, id)
	if err != nil {
		return err
	}

	targets := make([]writeTarget, 0, len(found))
	for _, f := range found {
		spec, merr := mergeUpdateSpec(f.silence, opts, now)
		if merr != nil {
			// A bad merge is the same for every found copy of the id, and
			// half-applying an explicit edit is worse than refusing it, so
			// abort before any write.
			return fmt.Errorf("silence %q in %q: %w", id, f.tenant, merr)
		}
		t := writeTarget{tenant: f.tenant, id: id, spec: spec}
		if f.silence.State == backend.SilenceStateExpired {
			t.skip = fmt.Errorf("silence is expired and cannot be updated; use `a10r silences recreate %s`", id)
		}
		targets = append(targets, t)
	}

	if opts.DryRun {
		return runDryRun(out, errOut, cfg, format, "update", targets, globalReadOnly)
	}
	if err := ensureWritableTargets(globalReadOnly, cfg, targetTenants(targets)); err != nil {
		return err
	}
	return runWrites(ctx, out, errOut, cfg, build, format, "updated", targets, updatedHint,
		func(ctx context.Context, c backend.Client, t writeTarget) (string, error) {
			if err := c.UpdateSilence(ctx, t.id, t.spec); err != nil {
				return "", fmt.Errorf("update silence: %w", err)
			}
			return t.id, nil
		})
}

// mergeUpdateSpec starts from the existing silence and applies only the
// supplied overrides, then validates the merged result the same way the
// backend would (matchers non-empty, comment non-empty, ends after
// starts) so a bad edit fails locally with a precise message. --ends is
// resolved relative to the post-merge start, so `--ends 2h` extends from
// wherever the silence now begins.
func mergeUpdateSpec(existing backend.Silence, opts silenceUpdateOptions, now time.Time) (backend.SilenceSpec, error) {
	spec := backend.SilenceSpec{
		Matchers:  existing.Matchers,
		StartsAt:  existing.StartsAt,
		EndsAt:    existing.EndsAt,
		CreatedBy: existing.CreatedBy,
		Comment:   existing.Comment,
	}
	if len(opts.Matchers) > 0 {
		ms, err := parseMatcherFlags(opts.Matchers)
		if err != nil {
			return backend.SilenceSpec{}, err
		}
		spec.Matchers = ms
	}
	if opts.Starts != "" {
		s, err := parseSilenceStart(opts.Starts, now)
		if err != nil {
			return backend.SilenceSpec{}, fmt.Errorf("--starts: %w", err)
		}
		spec.StartsAt = s
	}
	if opts.Ends != "" {
		e, err := parseSilenceEnd(opts.Ends, spec.StartsAt)
		if err != nil {
			return backend.SilenceSpec{}, fmt.Errorf("--ends: %w", err)
		}
		spec.EndsAt = e
	}
	if opts.Comment != "" {
		spec.Comment = opts.Comment
	}
	if opts.CreatedBy != "" {
		spec.CreatedBy = opts.CreatedBy
	}

	if len(spec.Matchers) == 0 {
		return backend.SilenceSpec{}, errors.New("at least one matcher is required")
	}
	if !spec.EndsAt.After(spec.StartsAt) {
		return backend.SilenceSpec{}, errors.New("ends must be after starts")
	}
	if strings.TrimSpace(spec.Comment) == "" {
		return backend.SilenceSpec{}, errors.New("comment is required")
	}
	return spec, nil
}
