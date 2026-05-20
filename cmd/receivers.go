// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/listcmd"
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

// runReceiversList is the per-command thin wrapper around
// listcmd.Run: parse flags, build a Spec whose Fetcher closure
// fans out the per-tenant ListReceivers call. Exit-code mapping
// lives here.
func runReceiversList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts receiversListOptions) error {
	format, err := output.ParseFormat(opts.Output)
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

	spec := listcmd.Spec[receiverRow]{
		Config: cfg,
		Format: format,
		Fetcher: func(ctx context.Context, name string, c backend.Client) ([]receiverRow, error) {
			recvs, err := c.ListReceivers(ctx)
			if err != nil {
				return nil, err
			}
			rows := make([]receiverRow, 0, len(recvs))
			for _, r := range recvs {
				rows = append(rows, toReceiverRow(name, r))
			}
			return rows, nil
		},
		Renderers: map[output.Format]listcmd.Renderer[receiverRow]{
			output.FormatTable: renderReceiverTable,
			output.FormatJSON:  renderReceiverJSON,
			output.FormatYAML:  renderReceiverYAML,
		},
		Sort:          sortReceiverRows,
		ResourceLabel: "receiver",
		FailOnAny:     opts.FailOnAny,
		NoPager:       flags.NoPager,
		Out:           out,
		Deps:          listcmd.Deps{BuildClient: build, PagerFactory: newPagerWriteCloser, Stderr: os.Stderr},
	}
	return mapPipelineExit(listcmd.Run(ctx, spec))
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

// renderReceiverRows dispatches to the chosen format. Kept as a
// thin shim for the existing unit tests; production wiring goes
// through the per-format Renderer map in runReceiversList.
func renderReceiverRows(out io.Writer, rows []receiverRow, format output.Format) error {
	switch format {
	case output.FormatJSON:
		return renderReceiverJSON(out, rows)
	case output.FormatYAML:
		return renderReceiverYAML(out, rows)
	case output.FormatTable:
		return renderReceiverTable(out, rows)
	}
	return fmt.Errorf("unknown format %q", format)
}

func renderReceiverJSON(out io.Writer, rows []receiverRow) error { return output.WriteJSON(out, rows) }
func renderReceiverYAML(out io.Writer, rows []receiverRow) error { return output.WriteYAML(out, rows) }

func renderReceiverTable(out io.Writer, rows []receiverRow) error {
	tbl := output.Table{
		Cols: []string{"tenant", "name"},
		Rows: receiverTableRows(rows),
	}
	return tbl.Write(out)
}

// receiverTableRows flattens to the column shape the Table helper
// consumes. Order matches Cols in renderReceiverTable.
func receiverTableRows(rows []receiverRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Tenant, r.Name})
	}
	return out
}
