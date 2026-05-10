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

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/output"
)

// newReceiversCmd returns the `a10r receivers` parent command. Mirror
// of newAlertsCmd / newSilencesCmd / newGroupsCmd: a single `list`
// verb today, future verbs (e.g. drill into alerts targeted at a
// receiver) reserved on the same noun-verb shape.
func newReceiversCmd(flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "receivers",
		Short:   "Inspect receivers across configured backends",
		GroupID: groupRead,
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newReceiversListCmd(flags))
	return cmd
}

// newReceiversListCmd is the headless complement to the receivers
// page. The AM /api/v2/receivers payload is just a list of names —
// no useful filter axis at this layer — so the command ships
// without filter flags. --fail returns ExitFailMatched (10) when at
// least one receiver was returned across the active scope; the use
// case is "fail my pipeline if no receivers are configured", a
// shape mirroring the alerts / silences --fail contract.
func newReceiversListCmd(flags *GlobalFlags) *cobra.Command {
	var (
		outputFmt string
		failOnAny bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List receivers across configured backends",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReceiversList(cmd.Context(), cmd.OutOrStdout(), flags, receiversListOptions{
				Output:    outputFmt,
				FailOnAny: failOnAny,
			})
		},
	}
	cmd.Flags().StringVar(&outputFmt, "output", "", "output format: table, json, yaml")
	cmd.Flags().BoolVar(&failOnAny, "fail", false,
		"exit with code 10 when at least one receiver is returned")
	return cmd
}

// receiversListOptions bundles the flag values so runReceiversList
// stays test-friendly without a cobra dependency.
type receiversListOptions struct {
	Output    string
	FailOnAny bool
}

// receiverRow is the row shape JSON / YAML / table all flatten the
// receiver payload into. Trivial today (Tenant + Name) but kept on
// the same struct shape as alertRow / silenceRow / groupRow so the
// JSON consumer story is uniform across the four list commands.
type receiverRow struct {
	Tenant string `json:"tenant" yaml:"tenant"`
	Name   string `json:"name" yaml:"name"`
}

// runReceiversList loads config, fans out ListReceivers across
// every configured backend, and renders. Same lenient
// partial-failure rule as runAlertsList (ADR 0009).
func runReceiversList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts receiversListOptions) error {
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

	rows, allFailed, fetchErrs := fetchReceiverRows(ctx, cfg, debugLog)
	for _, e := range fetchErrs {
		fmt.Fprintln(os.Stderr, e)
	}

	sortReceiverRows(rows)

	tty := isStdoutTerminal(out)
	resolved := output.Resolve(format, tty)
	pager, err := NewPager(ctx, out, tty && resolved == output.FormatTable, flags.NoPager)
	if err != nil {
		return err
	}
	if err := renderReceiverRows(pager, rows, resolved); err != nil {
		_ = pager.Close()
		return err
	}
	if err := pager.Close(); err != nil {
		return err
	}

	if opts.FailOnAny && len(rows) > 0 {
		return NewExitError(ExitFailMatched,
			fmt.Errorf("--fail: %d receiver(s) returned", len(rows)))
	}
	if allFailed {
		return NewExitError(ExitUnreachable, errors.New("every configured backend failed to list receivers"))
	}
	return nil
}

// fetchReceiverRows fans out ListReceivers across every configured
// backend, flattening each Receiver into a receiverRow tagged with
// the backend's name. Same partial-failure semantics as
// fetchAlertRows.
func fetchReceiverRows(ctx context.Context, cfg *config.Config, debugLog *slog.Logger) (rows []receiverRow, allFailed bool, errs []error) {
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
		recvs, err := c.ListReceivers(ctx)
		if err != nil {
			failed++
			errs = append(errs, fmt.Errorf("backend %q: list: %w", be.Name, err))
			continue
		}
		for _, r := range recvs {
			rows = append(rows, toReceiverRow(be.Name, r))
		}
	}
	return rows, failed == len(cfg.Backends), errs
}

// toReceiverRow flattens one backend.Receiver into the headless row
// shape, tagging it with the source backend name.
func toReceiverRow(tenant string, r backend.Receiver) receiverRow {
	return receiverRow{Tenant: tenant, Name: r.Name}
}

// sortReceiverRows orders rows for stable rendering: by tenant,
// then receiver name. Deterministic output makes diffs in CI logs
// meaningful.
func sortReceiverRows(rows []receiverRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Tenant != rows[j].Tenant {
			return rows[i].Tenant < rows[j].Tenant
		}
		return rows[i].Name < rows[j].Name
	})
}

// renderReceiverRows dispatches to the chosen format. Table
// flattens to TENANT / NAME columns; JSON / YAML emit the full
// receiverRow shape.
func renderReceiverRows(out io.Writer, rows []receiverRow, format output.Format) error {
	switch format {
	case output.FormatJSON:
		return output.WriteJSON(out, rows)
	case output.FormatYAML:
		return output.WriteYAML(out, rows)
	case output.FormatTable:
		// Fall through to table.
	}
	tbl := output.Table{
		Cols: []string{"tenant", "name"},
		Rows: receiverTableRows(rows),
	}
	return tbl.Write(out)
}

// receiverTableRows flattens to the column shape the Table helper
// consumes. Order matches Cols in renderReceiverRows.
func receiverTableRows(rows []receiverRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Tenant, r.Name})
	}
	return out
}
