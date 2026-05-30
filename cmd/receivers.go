// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/listcmd"
	"github.com/wilfriedroset/a10r/internal/output"
)

// newReceiversCmd returns the `a10r receivers` parent command.
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
	var common commonListFlags
	cmd := newListCmd("List receivers across configured backends",
		"exit with code 10 when at least one receiver is returned", &common)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runReceiversList(cmd.Context(), cmd.OutOrStdout(), flags, receiversListOptions{
			commonListFlags: common,
		})
	}
	return cmd
}

// receiversListOptions bundles the flag values so runReceiversList
// stays test-friendly without a cobra dependency.
type receiversListOptions struct {
	commonListFlags
}

// receiverRow is the row shape JSON / YAML / table all flatten
// the receiver payload into. Same struct shape as alertRow /
// silenceRow / groupRow so the JSON consumer story is uniform
// across the four list commands.
type receiverRow struct {
	Tenant string `json:"tenant" yaml:"tenant"`
	Name   string `json:"name" yaml:"name"`
}

// runReceiversList hands the receivers Fetcher to runListRecipe.
// Receivers carry no per-command filter so the Fetcher just flattens
// the wire response.
func runReceiversList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts receiversListOptions) error {
	return runListRecipe(ctx, out, flags, listRecipe[receiverRow]{
		Format: opts.Output,
		Fetcher: func(ctx context.Context, name string, c backend.Client) ([]receiverRow, error) {
			recvs, err := c.ListReceivers(ctx)
			if err != nil {
				return nil, fmt.Errorf("list receivers: %w", err)
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
	})
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

func renderReceiverJSON(out io.Writer, rows []receiverRow) error {
	if err := output.WriteJSON(out, rows); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

func renderReceiverYAML(out io.Writer, rows []receiverRow) error {
	if err := output.WriteYAML(out, rows); err != nil {
		return fmt.Errorf("write yaml: %w", err)
	}
	return nil
}

func renderReceiverTable(out io.Writer, rows []receiverRow) error {
	tbl := output.Table{
		Cols: []string{"tenant", "name"},
		Rows: receiverTableRows(rows),
	}
	if err := tbl.Write(out); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	return nil
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
