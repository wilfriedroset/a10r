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
)

// newSilencesCmd returns the `a10r silences` parent command.
func newSilencesCmd(flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "silences",
		Short:   "Inspect silences across configured backends",
		GroupID: groupRead,
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newSilencesListCmd(flags))
	cmd.AddCommand(newSilencesGetCmd(flags))
	cmd.AddCommand(newSilencesCreateCmd(flags))
	cmd.AddCommand(newSilencesUpdateCmd(flags))
	cmd.AddCommand(newSilencesExpireCmd(flags))
	cmd.AddCommand(newSilencesRecreateCmd(flags))
	return cmd
}

// newSilencesGetCmd is the headless complement to the TUI
// silence-detail page: fetch one silence by id and render its full
// payload. The rendered shape reuses silenceRow, so a silence reads the
// same whether it arrived via `silences list` or `silences get`, and
// its spec fields (matchers, starts/ends, comment, createdBy) line up
// with the editor buffer the TUI round-trips.
//
// The lookup is lenient across in-scope backends. A clean miss
// (ErrNotFound) on a backend is "the silence is not here", not a
// failure, so the absent backends stay silent and an everywhere-absent
// id exits ExitNotFound — distinct from the ExitUnreachable a genuine
// transport failure on every backend produces.
func newSilencesGetCmd(flags *GlobalFlags) *cobra.Command {
	var outputFormat string
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show full detail for one silence by id",
		Example: `  # Full detail for one silence (id from 'silences list')
  a10r silences get a1b2c3d4`,
		Args: exactlyOneArg("a silence id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSilenceGet(cmd.Context(), cmd.OutOrStdout(), flags, args[0], outputFormat)
		},
	}
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "",
		"output format: json, yaml (default: yaml on a terminal, json in a pipe); auto-JSON under an AI agent or A10R_OUTPUT")
	return cmd
}

// runSilenceGet is the cobra-facing entry: load+scope config, build the
// real client factory, then delegate to silenceGet. The split keeps
// silenceGet unit-testable with an injected fake factory.
func runSilenceGet(ctx context.Context, out io.Writer, flags *GlobalFlags, id, rawFormat string) error {
	format, err := resolveDetailFormat(rawFormat, os.Getenv, isStdoutTerminal(out))
	if err != nil {
		return err
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
	return silenceGet(ctx, out, os.Stderr, cfg, build, id, format)
}

// silenceGet fans out GetSilence by id across the in-scope backends,
// mapping ErrNotFound to an empty per-backend result (see the cobra
// command doc for why a clean miss is not a failure).
func silenceGet(
	ctx context.Context,
	out, errOut io.Writer,
	cfg *config.Config,
	build listcmd.ClientFactory,
	id string,
	format output.Format,
) error {
	results := fanOutBackends(ctx, cfg, build,
		func(ctx context.Context, tenant string, c backend.Client) ([]silenceRow, error) {
			s, err := c.GetSilence(ctx, id)
			if errors.Is(err, backend.ErrNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, fmt.Errorf("get silence: %w", err)
			}
			return []silenceRow{toSilenceRow(tenant, s)}, nil
		})
	return emitDetail(out, errOut, results, "silence", id, format)
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
		common      commonListFlags
		state       string
		matcherExpr string
	)
	cmd := newListCmd("List silences across configured backends",
		"exit with code 10 when at least one silence matches the filters", &common)
	cmd.Example = `  # Active silences across all tenants
  a10r silences list --state active

  # Silences matching a label, as JSON
  a10r silences list --matcher 'service="api"' -o json`
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runSilencesList(cmd.Context(), cmd.OutOrStdout(), flags, silencesListOptions{
			commonListFlags: common,
			State:           state,
			Matcher:         matcherExpr,
		})
	}
	cmd.Flags().StringVar(&state, "state", "",
		"keep only silences in the named state: active, pending, expired")
	cmd.Flags().StringVar(&matcherExpr, "matcher", "",
		`keep only silences whose matcher set contains the given Prom-style matcher (e.g. severity="critical")`)
	return cmd
}

// silencesListOptions bundles the flag values so runSilencesList
// stays test-friendly without a cobra dependency.
type silencesListOptions struct {
	commonListFlags
	State   string
	Matcher string
}

// silenceRow is the row shape JSON / YAML / table all flatten the
// silence payload into. Mirrors alertRow's documentation contract:
// struct tags pin the JSON key set per docs/end-users/output-
// formats.md.
//
// Matchers is *not* []backend.Matcher: the backend type carries no
// JSON tags so a direct embed leaks PascalCase Go field names onto
// the wire shape, breaking the "tenant / id / state / …" lowercase
// convention every other JSON key in this command set already
// follows. Wrapping in matcherRow keeps the public schema uniform
// across silences vs alerts vs groups vs receivers.
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

// runSilencesList validates --state and --matcher (so a typo errors
// without HTTP traffic) then hands the silence-specific Fetcher +
// filter wiring to runList.
func runSilencesList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts silencesListOptions) error {
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
	return runList(ctx, out, flags, opts.Output, listcmd.Spec[silenceRow]{
		Fetcher: func(ctx context.Context, name string, c backend.Client) ([]silenceRow, error) {
			silences, err := c.ListSilences(ctx, backend.SilenceFilter{})
			if err != nil {
				return nil, fmt.Errorf("list silences: %w", err)
			}
			rows := make([]silenceRow, 0, len(silences))
			for _, s := range silences {
				rows = append(rows, toSilenceRow(name, s))
			}
			return filterSilenceRows(rows, state, matcherFilter), nil
		},
		Renderers: map[output.Format]listcmd.Renderer[silenceRow]{
			output.FormatTable: renderSilenceTable,
			output.FormatJSON:  listcmd.JSONRenderer[silenceRow],
			output.FormatYAML:  listcmd.YAMLRenderer[silenceRow],
		},
		Sort:          sortSilenceRows,
		ResourceLabel: "silence",
		FailOnAny:     opts.FailOnAny,
	})
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

func renderSilenceTable(out io.Writer, rows []silenceRow) error {
	tbl := output.Table{
		Cols: []string{fieldTenant, "id", "state", "matchers", "ends-at"},
		Rows: silenceTableRows(rows),
	}
	if err := tbl.Write(out); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	return nil
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
