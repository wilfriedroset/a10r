// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/output"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// silenceCreateOptions bundles the create flags so silenceCreate stays
// testable without a cobra dependency.
type silenceCreateOptions struct {
	Matchers  []string
	Alerts    []string
	Starts    string
	Ends      string
	Comment   string
	CreatedBy string
	Output    string
	DryRun    bool
}

// newSilencesCreateCmd is the headless complement to the TUI silence
// form. Two prefill modes mirror the TUI: --matcher gives the matchers
// directly (the blank-form path), --alert <fingerprint> derives them
// from an alert instance's full label set (silence-one). The two are
// mutually exclusive.
//
// Targeting differs by mode. --alert derives its tenants: the silence
// lands in each in-scope backend where the fingerprint fires (a
// mirrored alert is silenced everywhere it shows). --matcher cannot
// derive a tenant, so when more than one backend is in scope it demands
// an explicit --tenant (`--tenant all` to fan out deliberately) rather
// than silently writing to every backend. Either way the write fails
// closed: if any resolved target is read-only, nothing is written.
func newSilencesCreateCmd(flags *GlobalFlags) *cobra.Command {
	var opts silenceCreateOptions
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create one or more silences from matchers or an alert fingerprint",
		Example: `  # Silence by matchers in one tenant
  a10r silences create --tenant prod --matcher 'severity="critical"' --comment maintenance

  # Silence an alert instance by fingerprint, ending in 4h
  a10r silences create --alert 1a2b3c4d5e6f7890 --ends 4h --comment "deploy in progress"

  # Preview the plan without writing, as JSON
  a10r silences create --tenant prod --matcher 'job="api"' --comment x --dry-run -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSilenceCreate(cmd.Context(), cmd.OutOrStdout(), flags, opts)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&opts.Matchers, "matcher", nil,
		`silence matcher in Prometheus syntax (repeatable), e.g. severity="critical"`)
	f.StringArrayVar(&opts.Alerts, "alert", nil,
		"fingerprint of an alert to silence by its full label set (repeatable); mutually exclusive with --matcher")
	f.StringVar(&opts.Starts, "starts", "",
		"silence start: now (default) or an RFC3339 timestamp")
	f.StringVar(&opts.Ends, "ends", "2h",
		"silence end: a duration (2h, 7d2h) added to the start, or an RFC3339 timestamp")
	f.StringVar(&opts.Comment, "comment", "", "silence comment (required)")
	f.StringVar(&opts.CreatedBy, "created-by", "", "silence author (default: $USER, else a10r)")
	f.StringVarP(&opts.Output, "output", "o", "",
		"output format: default tab-separated tenant<TAB>id, or json, yaml; auto-JSON under an AI agent or A10R_OUTPUT")
	f.BoolVar(&opts.DryRun, "dry-run", false,
		"resolve and print what would be written, without making any change")
	return cmd
}

// runSilenceCreate is the cobra-facing entry: resolve config (with the
// effective read-only knob), the client factory, the creator, and the
// clock, then delegate to the testable silenceCreate core.
func runSilenceCreate(ctx context.Context, out io.Writer, flags *GlobalFlags, opts silenceCreateOptions) error {
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
	format, err := resolveWriteFormat(opts.Output, os.Getenv)
	if err != nil {
		return err
	}
	explicitTenant := strings.TrimSpace(flags.Tenant) != ""
	return silenceCreate(ctx, out, os.Stderr, cfg, globalReadOnly, build,
		time.Now(), explicitTenant, opts, creator, format)
}

// silenceCreate validates the flags, resolves the target set, fails
// closed if any target is unwritable, and only then issues the writes.
func silenceCreate(
	ctx context.Context,
	out, errOut io.Writer,
	cfg *config.Config,
	globalReadOnly bool,
	build listcmd.ClientFactory,
	now time.Time,
	explicitTenant bool,
	opts silenceCreateOptions,
	creator string,
	format output.Format,
) error {
	if err := validateCreateFlags(opts); err != nil {
		return err
	}
	start, err := parseSilenceStart(opts.Starts, now)
	if err != nil {
		return fmt.Errorf("--starts: %w", err)
	}
	end, err := parseSilenceEnd(opts.Ends, start)
	if err != nil {
		return fmt.Errorf("--ends: %w", err)
	}
	if !end.After(start) {
		return errors.New("--ends must be after --starts")
	}

	targets, err := resolveCreateTargets(ctx, errOut, cfg, build, opts, explicitTenant, start, end, creator)
	if err != nil {
		return err
	}
	if opts.DryRun {
		return runDryRun(out, errOut, cfg, format, "create", targets, globalReadOnly)
	}
	if err := ensureWritableTargets(globalReadOnly, cfg, targetTenants(targets)); err != nil {
		return err
	}
	return runWrites(ctx, out, errOut, cfg, build, format, writeStatusCreated, targets, createdHint,
		func(ctx context.Context, c backend.Client, t writeTarget) (string, error) {
			return c.CreateSilence(ctx, t.spec)
		})
}

// validateCreateFlags enforces the matcher/alert XOR and the required
// comment before any backend work.
func validateCreateFlags(opts silenceCreateOptions) error {
	hasMatchers := len(opts.Matchers) > 0
	hasAlerts := len(opts.Alerts) > 0
	switch {
	case hasMatchers && hasAlerts:
		return errors.New("--matcher and --alert are mutually exclusive")
	case !hasMatchers && !hasAlerts:
		return errors.New("one of --matcher or --alert is required")
	}
	if strings.TrimSpace(opts.Comment) == "" {
		return errors.New("--comment is required")
	}
	return nil
}

// resolveCreateTargets builds the target list for whichever prefill
// mode is in play.
func resolveCreateTargets(
	ctx context.Context,
	errOut io.Writer,
	cfg *config.Config,
	build listcmd.ClientFactory,
	opts silenceCreateOptions,
	explicitTenant bool,
	start, end time.Time,
	creator string,
) ([]writeTarget, error) {
	if len(opts.Matchers) > 0 {
		return matcherTargets(cfg, opts, explicitTenant, start, end, creator)
	}
	return alertTargets(ctx, errOut, cfg, build, opts.Alerts, start, end, opts.Comment, creator)
}

// parseMatcherFlags parses each repeatable --matcher value as one
// Prometheus-style matcher. Shared by create and update so both report
// a bad matcher the same way.
func parseMatcherFlags(exprs []string) ([]backend.Matcher, error) {
	ms := make([]backend.Matcher, 0, len(exprs))
	for _, expr := range exprs {
		m, err := matcher.ParseOne(expr)
		if err != nil {
			return nil, fmt.Errorf("--matcher %q: %w", expr, err)
		}
		ms = append(ms, m)
	}
	return ms, nil
}

// matcherTargets parses the --matcher flags into one spec replicated
// across every in-scope backend. It refuses to fan out implicitly: with
// more than one backend in scope and no explicit --tenant the operator
// must say which (`--tenant all` opts in).
func matcherTargets(cfg *config.Config, opts silenceCreateOptions, explicitTenant bool, start, end time.Time, creator string) ([]writeTarget, error) {
	if !explicitTenant && len(cfg.Backends) > 1 {
		return nil, errors.New(
			"--matcher create would target every configured backend; pass --tenant <name|all|a,b> to choose",
		)
	}
	ms, err := parseMatcherFlags(opts.Matchers)
	if err != nil {
		return nil, err
	}
	spec := backend.SilenceSpec{
		Matchers:  ms,
		StartsAt:  start,
		EndsAt:    end,
		CreatedBy: creator,
		Comment:   opts.Comment,
	}
	targets := make([]writeTarget, 0, len(cfg.Backends))
	for _, be := range cfg.Backends {
		targets = append(targets, writeTarget{tenant: be.Name, spec: spec})
	}
	return targets, nil
}

// alertTargets reads the in-scope backends to resolve each requested
// fingerprint into (tenant, label-derived spec) targets. The lookup is
// lenient on transport failure but strict on the request: if any asked
// fingerprint resolves nowhere, nothing is silenced (ExitNotFound), so
// an explicit multi-alert request is never half-applied.
func alertTargets(
	ctx context.Context,
	errOut io.Writer,
	cfg *config.Config,
	build listcmd.ClientFactory,
	fingerprints []string,
	start, end time.Time,
	comment, creator string,
) ([]writeTarget, error) {
	want := make(map[string]bool, len(fingerprints))
	for _, fp := range fingerprints {
		want[fp] = true
	}

	spec := backend.SilenceSpec{StartsAt: start, EndsAt: end, CreatedBy: creator, Comment: comment}
	results := fanOutBackends(ctx, cfg, build,
		func(ctx context.Context, tenant string, c backend.Client) ([]alertHit, error) {
			alerts, err := c.ListAlerts(ctx, backend.AlertFilter{})
			if err != nil {
				return nil, fmt.Errorf("list alerts: %w", err)
			}
			return matchingAlertHits(tenant, alerts, want, spec), nil
		})
	failed, _ := emitBackendErrors(errOut, results)

	var targets []writeTarget
	found := make(map[string]bool, len(fingerprints))
	for _, r := range results {
		for _, h := range r.value {
			found[h.fingerprint] = true
			targets = append(targets, h.target)
		}
	}

	var missing []string
	for _, fp := range fingerprints {
		if !found[fp] {
			missing = append(missing, fp)
		}
	}
	// Strict on the request: any unresolved fingerprint aborts before a
	// single write, so an explicit multi-alert silence is never half-
	// applied. A missing fingerprint while some backend was unreachable
	// is "could not confirm" (it may live on the backend that failed) —
	// ExitUnreachable signals retry; only an all-reachable miss is the
	// definitive ExitNotFound.
	if len(missing) > 0 {
		if failed > 0 {
			return nil, NewExitError(ExitUnreachable,
				fmt.Errorf("a backend in scope failed; alert(s) %s not confirmed", strings.Join(missing, ", ")))
		}
		return nil, NewExitError(ExitNotFound,
			fmt.Errorf("alert(s) %s not found in scope", strings.Join(missing, ", ")))
	}
	return targets, nil
}

// alertHit pairs a resolved silence target with the requested
// fingerprint it came from, so alertTargets can tell which fingerprints
// went unresolved.
type alertHit struct {
	fingerprint string
	target      writeTarget
}

// matchingAlertHits turns one backend's alert list into the silence
// targets for the requested fingerprints, each carrying that instance's
// label-derived matchers atop the shared time/comment/creator template.
func matchingAlertHits(tenant string, alerts []backend.Alert, want map[string]bool, tmpl backend.SilenceSpec) []alertHit {
	var hits []alertHit
	for _, a := range alerts {
		if !want[a.Fingerprint] {
			continue
		}
		spec := tmpl
		spec.Matchers = matcher.FromLabels(a.Labels)
		hits = append(hits, alertHit{
			fingerprint: a.Fingerprint,
			target:      writeTarget{tenant: tenant, spec: spec},
		})
	}
	return hits
}

func parseSilenceStart(in string, now time.Time) (time.Time, error) {
	in = strings.TrimSpace(in)
	if in == "" || in == "now" {
		return now, nil
	}
	t, err := time.Parse(time.RFC3339, in)
	if err != nil {
		return time.Time{}, fmt.Errorf("not \"now\" or an RFC3339 timestamp: %q", in)
	}
	return t, nil
}

func parseSilenceEnd(in string, start time.Time) (time.Time, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return time.Time{}, errors.New("required: a duration like 2h or an RFC3339 timestamp")
	}
	if d, derr := timerender.Parse(in); derr == nil {
		return start.Add(d), nil
	}
	if t, terr := time.Parse(time.RFC3339, in); terr == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("not a duration (2h, 7d2h) or an RFC3339 timestamp: %q", in)
}

// ensureWritableTargets is the fail-closed gate shared by every write
// verb: a globally read-only session, or any individually read-only
// target backend, aborts the whole command before a single write. An
// explicit request that cannot be fully honoured is refused, not
// partially applied — the operator narrows --tenant to the writable set.
func ensureWritableTargets(globalReadOnly bool, cfg *config.Config, tenants []string) error {
	if globalReadOnly {
		return errors.New(
			"read-only mode is active (--read-only / A10R_READ_ONLY / defaults.read_only); silence writes are disabled",
		)
	}
	readOnly := make(map[string]bool, len(cfg.Backends))
	for _, be := range cfg.Backends {
		readOnly[be.Name] = be.ReadOnly
	}
	var ro []string
	for _, tn := range tenants {
		if readOnly[tn] {
			ro = append(ro, tn)
		}
	}
	if len(ro) > 0 {
		sort.Strings(ro)
		return fmt.Errorf(
			"read-only backend(s) in target set: %s; no silence was written (narrow --tenant to the writable set)",
			strings.Join(ro, ", "),
		)
	}
	return nil
}
