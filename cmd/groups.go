// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/listcmd"
	"github.com/wilfriedroset/a10r/internal/output"
)

// newGroupsCmd returns the `a10r groups` parent command.
func newGroupsCmd(flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "groups",
		Short:   "Inspect alert groups across configured backends",
		GroupID: groupRead,
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newGroupsListCmd(flags))
	return cmd
}

// newGroupsListCmd is the headless complement to the groups page.
// Fans out ListAlertGroups across every configured backend in scope
// and renders one row per group (NOT per alert — the value of the
// view is seeing how alerts cluster by their common label set).
//
// --receiver filters on the AM Receiver field of the contained
// alerts: a group survives when at least one of its alerts targets
// the named receiver. This keeps the predicate simple to reason
// about and matches the natural "show me groups feeding pager-duty"
// question. --fail returns ExitFailMatched (10) when at least one
// group survived the filters; ExitOK (0) otherwise. Label-selector
// filtering is deferred to a follow-up — see TODO in the package
// docs.
func newGroupsListCmd(flags *GlobalFlags) *cobra.Command {
	var (
		common   commonListFlags
		receiver string
	)
	cmd := newListCmd("List alert groups across configured backends",
		"exit with code 10 when at least one group matches the filters", &common)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runGroupsList(cmd.Context(), cmd.OutOrStdout(), flags, groupsListOptions{
			commonListFlags: common,
			Receiver:        receiver,
		})
	}
	cmd.Flags().StringVar(&receiver, "receiver", "",
		"keep only groups whose alerts target the named receiver (case-insensitive)")
	return cmd
}

// groupsListOptions bundles the flag values so runGroupsList stays
// test-friendly without a cobra dependency.
type groupsListOptions struct {
	commonListFlags
	Receiver string
}

// groupRow is the row shape JSON / YAML / table all flatten the
// alert-group payload into. Mirrors alertRow's documentation
// contract: struct tags pin the JSON key set per docs/end-users/
// output-formats.md.
//
// Receivers is the union of receivers across every alert in the
// group, deduplicated. Carried as a first-class field rather than a
// derived rendering so the JSON / YAML output is self-describing
// for downstream consumers.
type groupRow struct {
	Tenant    string            `json:"tenant" yaml:"tenant"`
	Labels    map[string]string `json:"labels" yaml:"labels"`
	Count     int               `json:"count" yaml:"count"`
	Receivers []string          `json:"receivers" yaml:"receivers"`
}

// runGroupsList hands the groups-specific Fetcher + filter wiring to
// runListRecipe; the filter runs inside the per-backend goroutine so
// the pipeline never sees an unfiltered slice.
func runGroupsList(ctx context.Context, out io.Writer, flags *GlobalFlags, opts groupsListOptions) error {
	return runListRecipe(ctx, out, flags, listRecipe[groupRow]{
		Format: opts.Output,
		Fetcher: func(ctx context.Context, name string, c backend.Client) ([]groupRow, error) {
			groups, err := c.ListAlertGroups(ctx, backend.AlertFilter{})
			if err != nil {
				return nil, fmt.Errorf("list alert groups: %w", err)
			}
			rows := make([]groupRow, 0, len(groups))
			for _, g := range groups {
				rows = append(rows, toGroupRow(name, g))
			}
			return filterGroupRows(rows, opts.Receiver), nil
		},
		Renderers: map[output.Format]listcmd.Renderer[groupRow]{
			output.FormatTable: renderGroupTable,
			output.FormatJSON:  renderGroupJSON,
			output.FormatYAML:  renderGroupYAML,
		},
		Sort:          sortGroupRows,
		ResourceLabel: "group",
		FailOnAny:     opts.FailOnAny,
	})
}

// toGroupRow flattens one backend.AlertGroup into the headless row
// shape. Receivers is the dedup'd union across the contained
// alerts so the table can show "where does this group fan out to"
// without expanding the alerts. Sort the receiver list so the
// rendered output is deterministic across polls.
func toGroupRow(tenant string, g backend.AlertGroup) groupRow {
	seen := map[string]struct{}{}
	receivers := make([]string, 0)
	for _, a := range g.Alerts {
		for _, r := range a.Receivers {
			if r == "" {
				continue
			}
			if _, ok := seen[r]; ok {
				continue
			}
			seen[r] = struct{}{}
			receivers = append(receivers, r)
		}
	}
	sort.Strings(receivers)
	return groupRow{
		Tenant:    tenant,
		Labels:    g.Labels,
		Count:     len(g.Alerts),
		Receivers: receivers,
	}
}

// filterGroupRows applies the --receiver filter in place. Empty
// filter is a no-op. Match is case-insensitive against the
// receiver names; a group survives when at least one of its alerts
// targets the named receiver.
func filterGroupRows(rows []groupRow, receiver string) []groupRow {
	if receiver == "" {
		return rows
	}
	out := rows[:0]
	for _, r := range rows {
		if !rowHasReceiver(r, receiver) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// rowHasReceiver reports whether row's Receivers union contains
// name (case-insensitive). Tiny predicate kept on its own seam so
// the filter loop reads as a single "skip when no match" line.
func rowHasReceiver(row groupRow, name string) bool {
	for _, r := range row.Receivers {
		if strings.EqualFold(r, name) {
			return true
		}
	}
	return false
}

// sortGroupRows orders rows for stable rendering: by tenant, then
// the rendered label summary, then count as a tiebreaker.
// Deterministic output makes diffs in CI logs meaningful.
func sortGroupRows(rows []groupRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Tenant != rows[j].Tenant {
			return rows[i].Tenant < rows[j].Tenant
		}
		li, lj := summariseLabels(rows[i].Labels), summariseLabels(rows[j].Labels)
		if li != lj {
			return li < lj
		}
		return rows[i].Count < rows[j].Count
	})
}

func renderGroupJSON(out io.Writer, rows []groupRow) error {
	if err := output.WriteJSON(out, rows); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

func renderGroupYAML(out io.Writer, rows []groupRow) error {
	if err := output.WriteYAML(out, rows); err != nil {
		return fmt.Errorf("write yaml: %w", err)
	}
	return nil
}

func renderGroupTable(out io.Writer, rows []groupRow) error {
	tbl := output.Table{
		Cols: []string{"tenant", "labels", "count", "receivers"},
		Rows: groupTableRows(rows),
	}
	if err := tbl.Write(out); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	return nil
}

// groupTableRows flattens to the column shape the Table helper
// consumes. Labels collapse to a deterministic comma-separated
// k=v summary so a multi-label group still fits one row;
// receivers join on `,` for the same reason.
func groupTableRows(rows []groupRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{
			r.Tenant,
			summariseLabels(r.Labels),
			strconv.Itoa(r.Count),
			strings.Join(r.Receivers, ","),
		})
	}
	return out
}

// summariseLabels renders a label map as a deterministic
// comma-separated `k=v` summary. Keys are sorted so the output is
// reproducible across map iteration randomness, and shared between
// the sort comparator and the table cell so the two views agree on
// row identity.
func summariseLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, ",")
}
