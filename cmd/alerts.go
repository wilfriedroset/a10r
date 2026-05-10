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

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/output"
)

// newAlertsCmd returns the `a10r alerts` parent command. The
// only child today is `list`; future verbs (silence, ack, …)
// hang off the same parent so the noun-verb shape stays
// consistent with kubectl / k9s.
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
		outputFmt string
		severity  string
		state     string
		failOnAny bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List alerts across configured backends",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAlertsList(cmd.Context(), cmd.OutOrStdout(), flags, alertsListOptions{
				Output:    outputFmt,
				Severity:  severity,
				State:     state,
				FailOnAny: failOnAny,
			})
		},
	}
	cmd.Flags().StringVar(&outputFmt, "output", "", "output format: table, json, yaml")
	cmd.Flags().StringVar(&severity, "severity", "",
		"keep only alerts matching the named severity label (case-insensitive)")
	cmd.Flags().StringVar(&state, "state", "",
		"keep only alerts in the named state: active, suppressed, unprocessed")
	cmd.Flags().BoolVar(&failOnAny, "fail", false,
		"exit with code 10 when at least one alert matches the filters")
	return cmd
}

// alertsListOptions bundles the flag values so runAlertsList
// stays test-friendly without a cobra dependency.
type alertsListOptions struct {
	Output    string
	Severity  string
	State     string
	FailOnAny bool
}

// alertRow is the row shape JSON / YAML / table all flatten the
// alert payload into. The struct tags pin the JSON key set as
// the v0.0.1 stability snapshot per docs/end-users/output-
// formats.md (which itself documents pre-v1 fluidity).
type alertRow struct {
	Tenant      string             `json:"tenant" yaml:"tenant"`
	Fingerprint string             `json:"fingerprint" yaml:"fingerprint"`
	State       backend.AlertState `json:"state" yaml:"state"`
	Severity    string             `json:"severity" yaml:"severity"`
	Name        string             `json:"name" yaml:"name"`
	Labels      map[string]string  `json:"labels" yaml:"labels"`
}

// runAlertsList loads config, fans out ListAlerts across every
// configured backend, applies the user's filters, and renders.
// Per-backend errors are surfaced to stderr but do not abort the
// run — the lenient partial-failure rule (ADR 0009) lives here
// so a single tenant blip doesn't break a multi-backend pipeline.
func runAlertsList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts alertsListOptions) error {
	format, err := output.ParseFormat(opts.Output)
	if err != nil {
		return err
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

	rows, allFailed, fetchErrs := fetchAlertRows(ctx, cfg, debugLog)
	for _, e := range fetchErrs {
		fmt.Fprintln(os.Stderr, e)
	}

	rows = filterAlertRows(rows, opts.Severity, opts.State)
	sortAlertRows(rows)

	tty := isStdoutTerminal(out)
	resolved := output.Resolve(format, tty)
	pager, err := NewPager(ctx, out, tty && resolved == output.FormatTable, flags.NoPager)
	if err != nil {
		return err
	}
	if err := renderAlertRows(pager, rows, resolved); err != nil {
		_ = pager.Close()
		return err
	}
	if err := pager.Close(); err != nil {
		return err
	}

	if opts.FailOnAny && len(rows) > 0 {
		return NewExitError(ExitFailMatched,
			fmt.Errorf("--fail: %d alert(s) matched the filter", len(rows)))
	}
	if allFailed {
		return NewExitError(ExitUnreachable, errors.New("every configured backend failed to list alerts"))
	}
	return nil
}

// fetchAlertRows fans out ListAlerts across every configured
// backend, flattening each Alert into an alertRow tagged with
// the backend's name. When debugLog is non-nil, factory.Build
// wraps each client with transport.WithDebugLog so --debug-http
// captures the per-tenant HTTP records. Returns the rows, an
// "every backend failed" boolean for the lenient partial-failure
// rule, and the per-backend errors so the caller can route them
// to stderr.
func fetchAlertRows(ctx context.Context, cfg *config.Config, debugLog *slog.Logger) (rows []alertRow, allFailed bool, errs []error) {
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
		alerts, err := c.ListAlerts(ctx, backend.AlertFilter{})
		if err != nil {
			failed++
			errs = append(errs, fmt.Errorf("backend %q: list: %w", be.Name, err))
			continue
		}
		for _, a := range alerts {
			rows = append(rows, toAlertRow(be.Name, a))
		}
	}
	return rows, failed == len(cfg.Backends), errs
}

// toAlertRow flattens one backend.Alert into the headless row
// shape. Severity is read from labels["severity"] (the
// Alertmanager convention) — empty when the label is absent.
func toAlertRow(tenant string, a backend.Alert) alertRow {
	severity := a.Labels["severity"]
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

// renderAlertRows dispatches to the chosen format. Table flattens
// to TENANT / NAME / SEVERITY / STATE columns; JSON / YAML emit
// the full alertRow shape including the labels map.
func renderAlertRows(out io.Writer, rows []alertRow, format output.Format) error {
	switch format {
	case output.FormatJSON:
		return output.WriteJSON(out, rows)
	case output.FormatYAML:
		return output.WriteYAML(out, rows)
	case output.FormatTable:
		// Fall through to table.
	}
	tbl := output.Table{
		Cols: []string{"tenant", "name", "severity", "state"},
		Rows: alertTableRows(rows),
	}
	return tbl.Write(out)
}

// alertTableRows flattens to the column shape the Table helper
// consumes. Order matches Cols in renderAlertRows.
func alertTableRows(rows []alertRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Tenant, r.Name, r.Severity, string(r.State)})
	}
	return out
}
