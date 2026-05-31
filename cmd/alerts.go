// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
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
// runListRecipe; the filter logic runs inside the per-backend goroutine
// so the pipeline never sees an unfiltered slice.
func runAlertsList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts alertsListOptions) error {
	return runListRecipe(ctx, out, flags, listRecipe[alertRow]{
		Format: opts.Output,
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
		Cols: []string{fieldTenant, fieldName, fieldSeverity, "state"},
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
		out = append(out, []string{r.Tenant, r.Name, r.Severity, string(r.State)})
	}
	return out
}
