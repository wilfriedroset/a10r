// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
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
	"github.com/wilfriedroset/a10r/internal/output"
)

// newAlertsCmd returns the `a10r alerts` parent command. Noun-
// verb shape stays consistent with kubectl / k9s.
func newAlertsCmd(flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "alerts",
		Short:   "Inspect alerts across configured backends",
		GroupID: groupRead,
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newAlertsListCmd(flags))
	cmd.AddCommand(newAlertsGetCmd(flags))
	return cmd
}

// newAlertsGetCmd is the headless complement to the TUI instance-detail
// (L3) page: fetch one alert instance by fingerprint and render its
// full payload — labels, annotations, generatorURL, and the suppression
// block (silenced-by / inhibited-by / muted-by). Fingerprint is the
// only stable instance identity Alertmanager exposes; alertname is a
// label, so "silence every HighCPU" is `silences create --matcher
// 'alertname="HighCPU"'`, not a get.
//
// The lookup is lenient across the in-scope backends (the same
// fingerprint can fire in several tenants when an alert is mirrored):
// every match is rendered, tenant-tagged. No match while some backend
// answered exits ExitNotFound; no match because every backend failed
// exits ExitUnreachable.
func newAlertsGetCmd(flags *GlobalFlags) *cobra.Command {
	var outputFormat string
	cmd := &cobra.Command{
		Use:   "get <fingerprint>",
		Short: "Show full detail for one alert instance by fingerprint",
		Args:  exactlyOneArg("an alert fingerprint"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAlertGet(cmd.Context(), cmd.OutOrStdout(), flags, args[0], outputFormat)
		},
	}
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "",
		"output format: json, yaml (default: yaml on a terminal, json in a pipe)")
	return cmd
}

// newAlertsListCmd is the headless complement to the TUI alerts
// page: fan out ListAlerts across every configured backend in
// scope and render the result table / json / yaml.
//
// --severity / --state flags filter on the corresponding label
// (severity) and AlertState. --fail returns ExitFailMatched (10)
// when at least one row survived the filters; ExitOK (0)
// otherwise. Wire CI gates do `a10r alerts list
// --severity=critical --fail || page-oncall`.
func newAlertsListCmd(flags *GlobalFlags) *cobra.Command {
	var (
		common   commonListFlags
		severity string
		state    string
	)
	cmd := newListCmd("List alerts across configured backends",
		"exit with code 10 when at least one alert matches the filters", &common)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runAlertsList(cmd.Context(), cmd.OutOrStdout(), flags, alertsListOptions{
			commonListFlags: common,
			Severity:        severity,
			State:           state,
		})
	}
	cmd.Flags().StringVar(&severity, fieldSeverity, "",
		"keep only alerts matching the named severity label (case-insensitive)")
	cmd.Flags().StringVar(&state, "state", "",
		"keep only alerts in the named state: active, suppressed, unprocessed")
	return cmd
}

// alertsListOptions bundles the flag values so runAlertsList
// stays test-friendly without a cobra dependency.
type alertsListOptions struct {
	commonListFlags
	Severity string
	State    string
}

// allowedAlertStates is the case-insensitive accept-list for --state.
// Validated up front (like silences list) so a typo such as `--state
// activ` errors instead of silently matching nothing — which on the
// `--fail` on-call path would read as an all-clear false negative.
var allowedAlertStates = []backend.AlertState{
	backend.AlertStateActive,
	backend.AlertStateSuppressed,
	backend.AlertStateUnprocessed,
}

// validateAlertState returns the canonical state when in is empty (no
// filter) or matches an allowed value case-insensitively, and a
// descriptive error otherwise.
func validateAlertState(in string) (string, error) {
	if in == "" {
		return "", nil
	}
	low := strings.ToLower(strings.TrimSpace(in))
	for _, s := range allowedAlertStates {
		if string(s) == low {
			return low, nil
		}
	}
	allowed := make([]string, 0, len(allowedAlertStates))
	for _, s := range allowedAlertStates {
		allowed = append(allowed, string(s))
	}
	return "", fmt.Errorf("unknown state %q (want one of %s)",
		in, strings.Join(allowed, ", "))
}

// alertRow is the row shape JSON / YAML / table all flatten the
// alert payload into. Struct tags pin the JSON key set per
// docs/end-users/output-formats.md.
type alertRow struct {
	Tenant      string             `json:"tenant" yaml:"tenant"`
	Fingerprint string             `json:"fingerprint" yaml:"fingerprint"`
	State       backend.AlertState `json:"state" yaml:"state"`
	Severity    string             `json:"severity" yaml:"severity"`
	Name        string             `json:"name" yaml:"name"`
	Labels      map[string]string  `json:"labels" yaml:"labels"`
}

// runAlertsList hands the alerts-specific Fetcher + filter wiring to
// runList; the filter logic runs inside the per-backend goroutine so
// the pipeline never sees an unfiltered slice.
func runAlertsList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts alertsListOptions) error {
	state, err := validateAlertState(opts.State)
	if err != nil {
		return err
	}
	opts.State = state
	return runList(ctx, out, flags, opts.Output, listcmd.Spec[alertRow]{
		Fetcher: func(ctx context.Context, name string, c backend.Client) ([]alertRow, error) {
			alerts, err := c.ListAlerts(ctx, backend.AlertFilter{})
			if err != nil {
				return nil, fmt.Errorf("list alerts: %w", err)
			}
			rows := make([]alertRow, 0, len(alerts))
			for _, a := range alerts {
				rows = append(rows, toAlertRow(name, a))
			}
			return filterAlertRows(rows, opts.Severity, opts.State), nil
		},
		Renderers: map[output.Format]listcmd.Renderer[alertRow]{
			output.FormatTable: renderAlertTable,
			output.FormatJSON:  listcmd.JSONRenderer[alertRow],
			output.FormatYAML:  listcmd.YAMLRenderer[alertRow],
		},
		Sort:          sortAlertRows,
		ResourceLabel: "alert",
		FailOnAny:     opts.FailOnAny,
	})
}

// toAlertRow flattens one backend.Alert into the headless row
// shape. Severity is read from labels["severity"] (the
// Alertmanager convention) — empty when the label is absent.
func toAlertRow(tenant string, a backend.Alert) alertRow {
	severity := a.Labels[fieldSeverity]
	name := a.Labels["alertname"]
	return alertRow{
		Tenant:      tenant,
		Fingerprint: a.Fingerprint,
		State:       a.State,
		Severity:    severity,
		Name:        name,
		Labels:      a.Labels,
	}
}

// filterAlertRows applies the --severity / --state filters in
// place. Empty filter strings are no-ops.
func filterAlertRows(rows []alertRow, severity, state string) []alertRow {
	if severity == "" && state == "" {
		return rows
	}
	out := rows[:0]
	for _, r := range rows {
		if severity != "" && !strings.EqualFold(r.Severity, severity) {
			continue
		}
		if state != "" && !strings.EqualFold(string(r.State), state) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// sortAlertRows orders rows for stable rendering: by tenant, then
// by alert name, then fingerprint as a tiebreaker. Deterministic
// output makes diffs in CI logs meaningful.
func sortAlertRows(rows []alertRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Tenant != rows[j].Tenant {
			return rows[i].Tenant < rows[j].Tenant
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Fingerprint < rows[j].Fingerprint
	})
}

func renderAlertTable(out io.Writer, rows []alertRow) error {
	tbl := output.Table{
		Cols: []string{fieldTenant, "fingerprint", fieldName, fieldSeverity, "state"},
		Rows: alertTableRows(rows),
	}
	if err := tbl.Write(out); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	return nil
}

// alertTableRows flattens to the column shape the Table helper
// consumes. Order matches Cols in renderAlertTable.
func alertTableRows(rows []alertRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Tenant, r.Fingerprint, r.Name, r.Severity, string(r.State)})
	}
	return out
}

// alertDetail is the full instance payload `alerts get` renders. Unlike
// alertRow (the list-column projection) it carries annotations, the
// generatorURL, and the suppression block, mirroring the TUI L3 page.
// omitempty keeps the rendered document tight when a field is absent
// (e.g. an active alert has no silenced-by list).
type alertDetail struct {
	Tenant       string             `json:"tenant" yaml:"tenant"`
	Fingerprint  string             `json:"fingerprint" yaml:"fingerprint"`
	State        backend.AlertState `json:"state" yaml:"state"`
	StartsAt     time.Time          `json:"startsAt" yaml:"startsAt"`
	EndsAt       time.Time          `json:"endsAt" yaml:"endsAt"`
	GeneratorURL string             `json:"generatorURL,omitempty" yaml:"generatorURL,omitempty"`
	Labels       map[string]string  `json:"labels" yaml:"labels"`
	Annotations  map[string]string  `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	SilencedBy   []string           `json:"silencedBy,omitempty" yaml:"silencedBy,omitempty"`
	InhibitedBy  []string           `json:"inhibitedBy,omitempty" yaml:"inhibitedBy,omitempty"`
	MutedBy      []string           `json:"mutedBy,omitempty" yaml:"mutedBy,omitempty"`
	Receivers    []string           `json:"receivers,omitempty" yaml:"receivers,omitempty"`
}

func toAlertDetail(tenant string, a backend.Alert) alertDetail {
	return alertDetail{
		Tenant:       tenant,
		Fingerprint:  a.Fingerprint,
		State:        a.State,
		StartsAt:     a.StartsAt,
		EndsAt:       a.EndsAt,
		GeneratorURL: a.GeneratorURL,
		Labels:       a.Labels,
		Annotations:  a.Annotations,
		SilencedBy:   a.SilencedBy,
		InhibitedBy:  a.InhibitedBy,
		MutedBy:      a.MutedBy,
		Receivers:    a.Receivers,
	}
}

// runAlertGet is the cobra-facing entry: load+scope config, build the
// real client factory, then delegate to alertGet. The split keeps
// alertGet free of config/factory wiring so it is unit-testable with an
// injected fake factory.
func runAlertGet(ctx context.Context, out io.Writer, flags *GlobalFlags, fingerprint, rawFormat string) error {
	format, err := resolveDetailFormat(rawFormat, isStdoutTerminal(out))
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
	return alertGet(ctx, out, os.Stderr, cfg, build, fingerprint, format)
}

// alertGet fans out a fingerprint lookup across the in-scope backends
// and renders every match. AM v2 has no get-by-fingerprint endpoint, so
// each backend lists and filters client-side; a backend contributes at
// most one match (fingerprint is unique within an Alertmanager).
func alertGet(
	ctx context.Context,
	out, errOut io.Writer,
	cfg *config.Config,
	build listcmd.ClientFactory,
	fingerprint string,
	format output.Format,
) error {
	results := fanOutBackends(ctx, cfg, build,
		func(ctx context.Context, tenant string, c backend.Client) ([]alertDetail, error) {
			alerts, err := c.ListAlerts(ctx, backend.AlertFilter{})
			if err != nil {
				return nil, fmt.Errorf("list alerts: %w", err)
			}
			var found []alertDetail
			for _, a := range alerts {
				if a.Fingerprint == fingerprint {
					found = append(found, toAlertDetail(tenant, a))
				}
			}
			return found, nil
		})
	return emitDetail(out, errOut, results, "alert", fingerprint, format)
}
