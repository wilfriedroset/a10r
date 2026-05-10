// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/output"
)

func TestToReceiverRow_PreservesShape(t *testing.T) {
	t.Parallel()

	got := toReceiverRow("prod", backend.Receiver{Name: "pager-duty"})
	require.Equal(t, "prod", got.Tenant)
	require.Equal(t, "pager-duty", got.Name)
}

func TestSortReceiverRows_TenantThenName(t *testing.T) {
	t.Parallel()

	rows := []receiverRow{
		{Tenant: "staging", Name: "alpha"},
		{Tenant: "prod", Name: "beta"},
		{Tenant: "prod", Name: "alpha"},
	}
	sortReceiverRows(rows)
	require.Equal(t, "prod", rows[0].Tenant)
	require.Equal(t, "alpha", rows[0].Name)
	require.Equal(t, "prod", rows[1].Tenant)
	require.Equal(t, "beta", rows[1].Name)
	require.Equal(t, "staging", rows[2].Tenant)
}

func TestRenderReceiverRows_TableHeaderAndCells(t *testing.T) {
	t.Parallel()

	rows := []receiverRow{
		{Tenant: "prod", Name: "pager-duty"},
	}
	var buf bytes.Buffer
	require.NoError(t, renderReceiverRows(&buf, rows, output.FormatTable))
	out := buf.String()
	require.Contains(t, out, "TENANT")
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "pager-duty")
}

func TestRenderReceiverRows_JSONShape(t *testing.T) {
	t.Parallel()

	rows := []receiverRow{
		{Tenant: "prod", Name: "pager-duty"},
	}
	var buf bytes.Buffer
	require.NoError(t, renderReceiverRows(&buf, rows, output.FormatJSON))
	out := buf.String()
	require.Contains(t, out, `"tenant": "prod"`)
	require.Contains(t, out, `"name": "pager-duty"`)
}

func TestReceiverTableRows_OrderMatchesCols(t *testing.T) {
	t.Parallel()

	got := receiverTableRows([]receiverRow{
		{Tenant: "prod", Name: "pager-duty"},
	})
	require.Len(t, got, 1)
	require.Equal(t, []string{"prod", "pager-duty"}, got[0])
}

// TestRunReceiversList_FailWhenAllBackendsDown mirrors the silences
// / groups tests: --fail against an unreachable backend exits
// ExitUnreachable.
func TestRunReceiversList_FailWhenAllBackendsDown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
backends:
  - name: down
    url: http://127.0.0.1:1
`), 0o600))

	flags := &GlobalFlags{ConfigPath: cfgPath}
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := runReceiversList(ctx, &buf, flags, receiversListOptions{
		Output:    "json",
		FailOnAny: true,
	})
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex, "must wrap ExitError")
	require.Equal(t, ExitUnreachable, ex.Code)
}
