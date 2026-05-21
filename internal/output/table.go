// SPDX-License-Identifier: Apache-2.0

package output

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Table holds a uniform set of rows for tabular rendering. Cols is
// the header sequence; every Rows[i] must carry exactly len(Cols)
// entries — Write returns an error rather than aligning best-effort.
//
// The shape is intentionally string-flat: per-command callers
// flatten typed payloads into rows themselves so the printer stays
// format-agnostic and unit-testable without importing the backend
// types. Numeric / temporal data is rendered to its display string
// at the call site (which already owns the formatting choices —
// "5m" vs "300s", absolute vs relative timestamps).
type Table struct {
	Cols []string
	Rows [][]string
}

// ErrRowWidth is returned by Write when a row's length does not
// match len(Cols). Surfaces as a programmer error: the call site
// is wrong, not the user.
var ErrRowWidth = errors.New("table row width mismatches column count")

// Tabwriter knobs. Promoted from inline magic numbers so the
// values are nameable from the doc comment and a future tweak
// (e.g. tighter padding for narrow terminals) only changes one
// site.
const (
	tabMinWidth = 0
	tabWidth    = 2
	tabPadding  = 2
	tabPadChar  = ' '
	tabFlags    = 0
)

// Write renders t to w using text/tabwriter at minwidth=0,
// tabwidth=2, padding=2, padchar=' ', flags=0. Headers are
// uppercased on output to match the TUI table convention without
// requiring the call site to pre-uppercase. Row-width validation
// happens inline during the render loop so the function stays
// single-pass — large outputs (10k+ rows) are common on Mimir
// tenants and a separate validation walk would double the cost.
//
// Tabwriter handles alignment for both terminals and pipes — there
// is no separate "TTY" code path today; lipgloss-styled headers can
// layer in later without touching this function's contract.
func (t Table) Write(w io.Writer) error {
	tw := tabwriter.NewWriter(w, tabMinWidth, tabWidth, tabPadding, tabPadChar, tabFlags)
	if _, err := fmt.Fprintln(tw, header(t.Cols)); err != nil {
		return fmt.Errorf("write table header: %w", err)
	}
	for i, r := range t.Rows {
		if len(r) != len(t.Cols) {
			return fmt.Errorf("%w: row %d has %d cells, want %d",
				ErrRowWidth, i, len(r), len(t.Cols))
		}
		if _, err := fmt.Fprintln(tw, strings.Join(r, "\t")); err != nil {
			return fmt.Errorf("write table row %d: %w", i, err)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}
	return nil
}

// header tab-joins cols after uppercasing each. Kept as a small
// pure helper so the rendering loop stays readable and the test
// can assert column casing in isolation.
func header(cols []string) string {
	upper := make([]string, len(cols))
	for i, c := range cols {
		upper[i] = strings.ToUpper(c)
	}
	return strings.Join(upper, "\t")
}
