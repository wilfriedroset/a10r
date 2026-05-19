// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/output"
)

// newSilencesCmd returns the `a10r silences` parent command. Mirror
// of newAlertsCmd: a single `list` verb today, with future verbs
// (create, expire) reserved on the same noun-verb shape so the help
// reads consistently across the read-list pages.
func newSilencesCmd(flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "silences",
		Short:   "Inspect silences across configured backends",
		GroupID: groupRead,
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newSilencesListCmd(flags))
	return cmd
}

// newSilencesListCmd is the headless complement to the silences page.
// Fans out ListSilences across every configured backend in scope and
// renders the result as table / json / yaml.
//
// --state filters on Silence.State (active / pending / expired); the
// validation gate is fail-closed so a typo surfaces immediately
// rather than silently matching nothing. --matcher accepts a single
// Prom-style matcher (`severity="critical"`, `team!~"infra-.*"`)
// applied as an in-process predicate over the silence's matcher set
// — the AM /api/v2/silences endpoint takes a matcher filter on the
// wire too, but applying it client-side keeps the helper testable
// without round-tripping HTTP. --fail returns ExitFailMatched (10)
// when at least one row survived the filters; ExitOK (0) otherwise.
func newSilencesListCmd(flags *GlobalFlags) *cobra.Command {
	var (
		outputFmt string
		state     string
		matcher   string
		failOnAny bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List silences across configured backends",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSilencesList(cmd.Context(), cmd.OutOrStdout(), flags, silencesListOptions{
				Output:    outputFmt,
				State:     state,
				Matcher:   matcher,
				FailOnAny: failOnAny,
			})
		},
	}
	cmd.Flags().StringVar(&outputFmt, "output", "", "output format: table, json, yaml")
	cmd.Flags().StringVar(&state, "state", "",
		"keep only silences in the named state: active, pending, expired")
	cmd.Flags().StringVar(&matcher, "matcher", "",
		`keep only silences whose matcher set contains the given Prom-style matcher (e.g. severity="critical")`)
	cmd.Flags().BoolVar(&failOnAny, "fail", false,
		"exit with code 10 when at least one silence matches the filters")
	return cmd
}

// silencesListOptions bundles the flag values so runSilencesList
// stays test-friendly without a cobra dependency.
type silencesListOptions struct {
	Output    string
	State     string
	Matcher   string
	FailOnAny bool
}

// silenceRow is the row shape JSON / YAML / table all flatten the
// silence payload into. Mirrors alertRow's documentation contract:
// the struct tags pin the JSON key set as the v0.0.1 stability
// snapshot per docs/end-users/output-formats.md.
//
// Matchers is *not* []backend.Matcher: the backend type carries no
// JSON tags so a direct embed leaks PascalCase Go field names onto
// the v0.0.1 wire shape, breaking the "tenant / id / state / …"
// lowercase convention every other JSON key in this command set
// already follows. Wrapping in matcherRow keeps the public schema
// uniform across silences vs alerts vs groups vs receivers.
type silenceRow struct {
	Tenant    string               `json:"tenant" yaml:"tenant"`
	ID        string               `json:"id" yaml:"id"`
	State     backend.SilenceState `json:"state" yaml:"state"`
	CreatedBy string               `json:"createdBy" yaml:"createdBy"`
	Comment   string               `json:"comment" yaml:"comment"`
	StartsAt  time.Time            `json:"startsAt" yaml:"startsAt"`
	EndsAt    time.Time            `json:"endsAt" yaml:"endsAt"`
	Matchers  []matcherRow         `json:"matchers" yaml:"matchers"`
}

// matcherRow is the JSON / YAML projection of backend.Matcher.
// Declared here (rather than annotating backend.Matcher with tags
// directly) so the headless wire shape stays decoupled from the
// in-memory backend type — adding a field on backend.Matcher for
// the TUI would otherwise quietly ship through to --output=json.
type matcherRow struct {
	Name    string `json:"name" yaml:"name"`
	Value   string `json:"value" yaml:"value"`
	IsRegex bool   `json:"isRegex" yaml:"isRegex"`
	IsEqual bool   `json:"isEqual" yaml:"isEqual"`
}

// allowedSilenceStates is the case-insensitive accept-list for
// --state. Mirrors backend.SilenceState* constants but documented
// here so `--state foo` fails with a usable error string instead of
// "no rows" silence.
var allowedSilenceStates = []backend.SilenceState{
	backend.SilenceStateActive,
	backend.SilenceStatePending,
	backend.SilenceStateExpired,
}

// validateSilenceState returns the canonical state string when in is
// empty (no filter) or matches one of the allowed values
// case-insensitively, and a descriptive error otherwise. The
// validation runs before any HTTP traffic so a typo costs the user
// nothing.
func validateSilenceState(in string) (string, error) {
	if in == "" {
		return "", nil
	}
	low := strings.ToLower(strings.TrimSpace(in))
	for _, s := range allowedSilenceStates {
		if string(s) == low {
			return low, nil
		}
	}
	allowed := make([]string, 0, len(allowedSilenceStates))
	for _, s := range allowedSilenceStates {
		allowed = append(allowed, string(s))
	}
	return "", fmt.Errorf("unknown state %q (want one of %s)",
		in, strings.Join(allowed, ", "))
}

// runSilencesList loads config, fans out ListSilences across every
// configured backend, applies the user's filters, and renders. Same
// lenient partial-failure rule as runAlertsList (ADR 0009) — a
// single tenant blip does not abort a multi-backend run.
func runSilencesList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts silencesListOptions) error {
	format, err := output.ParseFormat(opts.Output)
	if err != nil {
		return err
	}
	state, err := validateSilenceState(opts.State)
	if err != nil {
		return err
	}
	var matcherFilter *backend.Matcher
	if opts.Matcher != "" {
		m, perr := matcher.ParseOne(opts.Matcher)
		if perr != nil {
			return fmt.Errorf("--matcher: %w", perr)
		}
		matcherFilter = &m
	}

	cfg, err := config.Load(loadOptsFromFlags(flags))
	if err != nil {
		return NewExitError(ExitConfigInvalid, fmt.Errorf("load config: %w", err))
	}

	debugLog, closer, err := buildHTTPDebugLogger(flags, os.Stderr)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()

	rows, allFailed, fetchErrs := fetchSilenceRows(ctx, cfg, debugLog)
	for _, e := range fetchErrs {
		fmt.Fprintln(os.Stderr, e)
	}

	rows = filterSilenceRows(rows, state, matcherFilter)
	sortSilenceRows(rows)

	tty := isStdoutTerminal(out)
	resolved := output.Resolve(format, tty)
	pager, err := NewPager(ctx, out, tty && resolved == output.FormatTable, flags.NoPager)
	if err != nil {
		return err
	}
	if err := renderSilenceRows(pager, rows, resolved); err != nil {
		_ = pager.Close()
		return err
	}
	if err := pager.Close(); err != nil {
		return err
	}

	if opts.FailOnAny && len(rows) > 0 {
		return NewExitError(ExitFailMatched,
			fmt.Errorf("--fail: %d silence(s) matched the filter", len(rows)))
	}
	if allFailed {
		return NewExitError(ExitUnreachable, errors.New("every configured backend failed to list silences"))
	}
	return nil
}

// fetchSilenceRows fans out ListSilences across every configured
// backend. Same partial-failure semantics as fetchAlertRows: the
// "every backend failed" boolean drives the ExitUnreachable branch,
// per-backend errors route to stderr.
func fetchSilenceRows(ctx context.Context, cfg *config.Config, debugLog *slog.Logger) (rows []silenceRow, allFailed bool, errs []error) {
	if len(cfg.Backends) == 0 {
		return nil, false, nil
	}
	failed := 0
	ua := userAgent(version, commit)
	var opts []factory.Option
	if debugLog != nil {
		opts = append(opts, factory.WithDebugLog(debugLog))
	}
	for _, be := range cfg.Backends {
		c, err := factory.Build(be, ua, opts...)
		if err != nil {
			failed++
			errs = append(errs, fmt.Errorf("backend %q: build: %w", be.Name, err))
			continue
		}
		silences, err := c.ListSilences(ctx, backend.SilenceFilter{})
		if err != nil {
			failed++
			errs = append(errs, fmt.Errorf("backend %q: list: %w", be.Name, err))
			continue
		}
		for _, s := range silences {
			rows = append(rows, toSilenceRow(be.Name, s))
		}
	}
	return rows, failed == len(cfg.Backends), errs
}

// toSilenceRow flattens one backend.Silence into the headless row
// shape, tagging it with the source backend name. Matchers are
// shallow-copied through the matcherRow projection so the public
// JSON / YAML keys are lower-camel and the row's matcher slice
// does not alias the source backend.Silence's slice.
func toSilenceRow(tenant string, s backend.Silence) silenceRow {
	matchers := make([]matcherRow, 0, len(s.Matchers))
	for _, m := range s.Matchers {
		matchers = append(matchers, matcherRow{
			Name:    m.Name,
			Value:   m.Value,
			IsRegex: m.IsRegex,
			IsEqual: m.IsEqual,
		})
	}
	return silenceRow{
		Tenant:    tenant,
		ID:        s.ID,
		State:     s.State,
		CreatedBy: s.CreatedBy,
		Comment:   s.Comment,
		StartsAt:  s.StartsAt,
		EndsAt:    s.EndsAt,
		Matchers:  matchers,
	}
}

// filterSilenceRows applies the --state / --matcher filters in
// place. Empty state and nil matcher are no-ops. The matcher
// predicate is "the silence carries at least one Matcher whose
// (name, value, isRegex, isEqual) tuple equals the supplied one"
// — strict structural equality so the user gets predictable
// behaviour across the wire-format edge cases ("severity=~critical"
// is NOT the same matcher as "severity=critical" even though both
// happen to match the value `critical`).
func filterSilenceRows(rows []silenceRow, state string, m *backend.Matcher) []silenceRow {
	if state == "" && m == nil {
		return rows
	}
	out := rows[:0]
	for _, r := range rows {
		if state != "" && !strings.EqualFold(string(r.State), state) {
			continue
		}
		if m != nil && !matcherSliceContains(r.Matchers, *m) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// matcherSliceContains reports whether ms carries an element whose
// operator-aware tuple matches m. Equality is structural over the
// four matcher fields. Pure predicate over the slice (no silenceRow
// wrapper) so callers that only have a []matcherRow can reuse it
// without round-tripping the rendering shape.
func matcherSliceContains(ms []matcherRow, m backend.Matcher) bool {
	for _, candidate := range ms {
		if candidate.Name == m.Name &&
			candidate.Value == m.Value &&
			candidate.IsRegex == m.IsRegex &&
			candidate.IsEqual == m.IsEqual {
			return true
		}
	}
	return false
}

// sortSilenceRows orders rows for stable rendering: by tenant, then
// state (active first by alphabetical chance — "active" < "expired"
// < "pending"), then ID as a tiebreaker. Deterministic output makes
// diffs in CI logs meaningful.
func sortSilenceRows(rows []silenceRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Tenant != rows[j].Tenant {
			return rows[i].Tenant < rows[j].Tenant
		}
		if rows[i].State != rows[j].State {
			return rows[i].State < rows[j].State
		}
		return rows[i].ID < rows[j].ID
	})
}

// renderSilenceRows dispatches to the chosen format. Table flattens
// to TENANT / ID / STATE / MATCHERS / ENDS-AT columns; the matcher
// column collapses the matcher slice into a single Prom-style
// summary so a multi-matcher silence still fits one row. JSON / YAML
// emit the full silenceRow shape including the matcher array.
func renderSilenceRows(out io.Writer, rows []silenceRow, format output.Format) error {
	switch format {
	case output.FormatJSON:
		return output.WriteJSON(out, rows)
	case output.FormatYAML:
		return output.WriteYAML(out, rows)
	case output.FormatTable:
		// Fall through to table.
	}
	tbl := output.Table{
		Cols: []string{"tenant", "id", "state", "matchers", "ends-at"},
		Rows: silenceTableRows(rows),
	}
	return tbl.Write(out)
}

// silenceTableRows flattens to the column shape the Table helper
// consumes. EndsAt renders as RFC3339 in UTC for a stable
// human-readable timestamp; matcher slices collapse to a single
// comma-separated Prom-style summary.
func silenceTableRows(rows []silenceRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{
			r.Tenant,
			r.ID,
			string(r.State),
			summariseMatchers(r.Matchers),
			r.EndsAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// summariseMatchers renders a matcher slice as a comma-separated
// `name<op>"value"` summary. Mirrors the Prom convention so a
// rendered cell can be pasted back into --matcher round-trip.
func summariseMatchers(ms []matcherRow) string {
	if len(ms) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		op := matcher.Op(backend.Matcher{IsRegex: m.IsRegex, IsEqual: m.IsEqual})
		parts = append(parts, m.Name+op+`"`+m.Value+`"`)
	}
	return strings.Join(parts, ",")
}
