// SPDX-License-Identifier: Apache-2.0

package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTable_Write_HappyPath(t *testing.T) {
	t.Parallel()

	tbl := Table{
		Cols: []string{"name", "severity"},
		Rows: [][]string{
			{"HighCPU", "critical"},
			{"DiskFull", "warning"},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, tbl.Write(&buf))

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3, "header + 2 data rows")
	require.Contains(t, lines[0], "NAME")
	require.Contains(t, lines[0], "SEVERITY")
	require.Contains(t, lines[1], "HighCPU")
	require.Contains(t, lines[2], "DiskFull")
}

func TestTable_Write_PropagatesWriterError(t *testing.T) {
	t.Parallel()

	// failingWriter (defined in yaml_test.go) returns errStub on
	// every Write. The wrapped error surfaces from one of three
	// sites depending on tabwriter's flush behaviour for the
	// payload size: header write, row write, or Flush. The
	// underlying error is the same; assert errors.Is rather than
	// pinning a specific wrap prefix (which would be brittle).
	tbl := Table{Cols: []string{"a"}, Rows: [][]string{{"x"}}}
	err := tbl.Write(failingWriter{})
	require.Error(t, err)
	require.ErrorIs(t, err, errStub, "the underlying writer error must propagate")
}

func TestTable_Write_RejectsRowWidthMismatch(t *testing.T) {
	t.Parallel()

	tbl := Table{
		Cols: []string{"a", "b", "c"},
		Rows: [][]string{
			{"1", "2", "3"},
			{"4", "5"}, // missing one cell
		},
	}
	var buf bytes.Buffer
	err := tbl.Write(&buf)
	require.ErrorIs(t, err, ErrRowWidth)
}

func TestTable_Write_EmptyRowsHeaderOnly(t *testing.T) {
	t.Parallel()

	tbl := Table{Cols: []string{"name"}, Rows: nil}
	var buf bytes.Buffer
	require.NoError(t, tbl.Write(&buf))

	require.Equal(t, "NAME\n", buf.String())
}

func TestTable_Write_ColumnsLineUp(t *testing.T) {
	t.Parallel()

	// tabwriter pads each column to the width of its widest cell.
	// Verify alignment by computing column-start offsets from the
	// header line and asserting the second-column offset is
	// identical on every data row. A regression that emitted the
	// rows without tab expansion would shift the second column
	// and trip this assertion.
	tbl := Table{
		Cols: []string{"short", "longer-col"},
		Rows: [][]string{
			{"a", "x"},
			{"abc", "yyyyy"},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, tbl.Write(&buf))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)

	headerCol2 := strings.Index(lines[0], "LONGER-COL")
	require.GreaterOrEqual(t, headerCol2, 0, "header line carries second column")

	row1Col2 := strings.Index(lines[1], "x")
	row2Col2 := strings.Index(lines[2], "yyyyy")
	require.Equal(t, headerCol2, row1Col2,
		"row 1 second column starts at the header offset")
	require.Equal(t, headerCol2, row2Col2,
		"row 2 second column starts at the header offset")
}
