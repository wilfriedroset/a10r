// SPDX-License-Identifier: Apache-2.0

package format

import "sort"

// Column describes how a single column participates in width
// distribution. The allocator reads three knobs:
//
//   - Min: hard floor on the assigned width. The column never
//     shrinks below this, even when the terminal is narrower than
//     the table needs. Use for columns whose glyph (e.g. a
//     severity dot or a state badge) must always be readable.
//   - Content: the widest cell the column would render at full
//     fidelity (max over the row dataset, including the header
//     label). The allocator never assigns more than Content to a
//     column — extra width is parked in slack instead.
//   - Weight: distribution weight for the leftover budget. Zero
//     means "fixed": the column gets exactly max(Min, Content) and
//     never grows past it. Positive weights share the remainder
//     proportionally.
//
// The allocator is content-bounded: a flex column whose Content is
// already satisfied gives its surplus weight share back to other
// flex columns until either everyone is satisfied or the budget
// is empty.
type Column struct {
	Min     int
	Content int
	Weight  int
}

// Distribute returns the per-column widths that fit total terminal
// cells, given the column specs. The algorithm mirrors duf's
// computeAssignedWidths (see github.com/muesli/duf/table.go) but
// stays a small pure function so the alerts page (and any future
// table) can call it without dragging in a forked dependency.
//
// Steps:
//  1. Reserve max(Min, Content) for every fixed column (Weight == 0)
//     and Min for every flex column (Weight > 0).
//  2. Subtract reserved + separators from total to get the flex
//     remainder. If the reservation already overruns total, the
//     algorithm pro-rata shrinks fixed columns toward Min so the
//     row still fits — never returns a width sum greater than total.
//  3. Distribute the remainder across flex columns by weight,
//     capping each at Content. Surplus from capped columns rolls
//     into a second-pass redistribution among uncapped peers.
//  4. After distribution, every column is at most Content cells
//     wide; the caller is expected to pass each cell through
//     Truncate / SGRTruncate (with EllipsizeSuffix on the flex
//     column) to ellipsize on assignment shortfall.
//
// total is the gross width budget INCLUDING the separator overhead
// the renderer will spend between columns. separator is the width
// of one inter-column separator (e.g. 0 if columns are pre-padded
// to their assigned widths and concatenated, 1 if a space is
// inserted between cells). Returns nil for an empty cols slice and
// a same-length slice otherwise.
func Distribute(cols []Column, total, separator int) []int {
	if len(cols) == 0 {
		return nil
	}
	if total < 0 {
		total = 0
	}
	out := make([]int, len(cols))

	// Separators eat budget before any column does. Two columns =>
	// one separator, n columns => n-1 separators. Negative or zero
	// gross width after separator overhead means the table is too
	// narrow to render meaningfully — every column collapses to 0.
	sepBudget := max(0, (len(cols)-1)*max(0, separator))
	budget := total - sepBudget
	if budget <= 0 {
		return out
	}

	// Step 1: reservation. Fixed columns claim max(Min, Content);
	// flex columns claim Min. Negative inputs are clamped to 0 so
	// callers can pass uninitialised structs without surprising
	// the allocator.
	reserved := 0
	for i, c := range cols {
		minW := max(0, c.Min)
		contentW := max(0, c.Content)
		if c.Weight <= 0 {
			out[i] = max(minW, contentW)
		} else {
			out[i] = minW
		}
		reserved += out[i]
	}

	// Step 2: budget overrun. Reservation already wider than the
	// terminal — shrink everyone proportionally toward 0 so the
	// row never overflows. Falling-back to per-column floors first
	// would create rows wider than total on a 20-cell terminal,
	// breaking the contract that sum(out) <= total.
	if reserved > budget {
		shrinkProportional(out, budget)
		return out
	}

	// Step 3: weight-driven distribution of the remainder. We loop
	// because capping a flex column at its Content frees its share
	// for the others — a single pass would leave that surplus on
	// the floor.
	distributeFlex(cols, out, budget-reserved)
	return out
}

// distributeFlex hands the remainder cells to the flex columns in
// out, weighted by Column.Weight and capped at Column.Content.
// Loops until either the budget is empty or every flex column is
// satisfied; integer-division residuals fall through to
// distributeTail so a 1-3 cell tail still finds a home.
func distributeFlex(cols []Column, out []int, remainder int) {
	for remainder > 0 {
		weightSum := flexWeightSum(cols, out)
		if weightSum == 0 {
			return // no flex column wants more cells; park slack
		}
		distributed := distributeOnce(cols, out, remainder, weightSum)
		if distributed == 0 {
			// Integer division dropped every share to 0 (remainder
			// smaller than weightSum). Hand the leftover cells out
			// one at a time to the highest-weight uncapped column —
			// guarantees forward progress and keeps the allocator
			// from looping forever on a 1-cell residual.
			distributed = distributeTail(cols, out, remainder)
		}
		if distributed == 0 {
			return
		}
		remainder -= distributed
	}
}

// flexWeightSum totals the weights of flex columns whose assigned
// width is still below their content cap. Capped columns drop out
// so the remaining budget rolls onto the uncapped peers.
func flexWeightSum(cols []Column, out []int) int {
	total := 0
	for i, c := range cols {
		if c.Weight > 0 && out[i] < max(0, c.Content) {
			total += c.Weight
		}
	}
	return total
}

// distributeOnce performs a single weighted-share pass: each
// uncapped flex column gets remainder*Weight/weightSum cells,
// clamped by its Content room. Returns the number of cells handed
// out so the caller can subtract from the running remainder.
func distributeOnce(cols []Column, out []int, remainder, weightSum int) int {
	distributed := 0
	for i, c := range cols {
		if c.Weight <= 0 {
			continue
		}
		contentW := max(0, c.Content)
		if out[i] >= contentW {
			continue
		}
		share := remainder * c.Weight / weightSum
		room := contentW - out[i]
		share = min(share, room)
		out[i] += share
		distributed += share
	}
	return distributed
}

// shrinkProportional reduces every entry in out so the total
// equals budget. Used when the column reservation already exceeds
// the terminal width — every column donates pro-rata so no single
// column collapses to 0 while another stays full. The integer
// residual after rounding is patched onto the widest column so
// sum(out) hits budget exactly.
func shrinkProportional(out []int, budget int) {
	total := 0
	for _, w := range out {
		total += w
	}
	if total <= 0 || budget <= 0 {
		for i := range out {
			out[i] = 0
		}
		return
	}
	used := 0
	widest := 0
	for i, w := range out {
		scaled := w * budget / total
		out[i] = scaled
		used += scaled
		if out[widest] < out[i] {
			widest = i
		}
	}
	if used < budget {
		out[widest] += budget - used
	}
}

// distributeTail hands the leftover cells out one at a time among
// uncapped flex columns. The integer-division pass in
// distributeOnce can leave a 1-N cell tail that no proportional
// share would claim; round-robin awarding by weight order
// guarantees forward progress and keeps the allocator from
// spinning on the same residual.
//
// Highest-weight columns get the first cell each pass (sorted
// stable, so caller order wins ties — "the column listed first
// gets the leftover"). Successive passes loop until the remainder
// is exhausted or every uncapped column has hit its content cap.
func distributeTail(cols []Column, out []int, remainder int) int {
	if remainder <= 0 {
		return 0
	}
	order := make([]int, 0, len(cols))
	for i, c := range cols {
		if c.Weight > 0 && out[i] < max(0, c.Content) {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return cols[order[a]].Weight > cols[order[b]].Weight
	})
	given := 0
	for given < remainder {
		progressed := false
		for _, i := range order {
			if given >= remainder {
				break
			}
			contentW := max(0, cols[i].Content)
			if out[i] >= contentW {
				continue
			}
			out[i]++
			given++
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return given
}

// EllipsizeSuffix is the single-cell suffix Truncate / SGRTruncate
// callers append when a flex column's Content exceeds its assigned
// width. Centralised here so every table page agrees on the glyph
// without re-importing it from the allocator's neighbour helpers.
const EllipsizeSuffix = "…"

// Ellipsize clips s to at most w terminal cells, replacing the
// final cell with EllipsizeSuffix when truncation actually
// occurred. Returns "" for w <= 0; returns s unchanged when its
// rendered width already fits. Not SGR-aware — pre-styled cells
// must call SGRTruncate directly and ellipsize themselves, since
// the suffix would otherwise land outside any active style.
//
// w == 1 returns the suffix alone — the only correct rendering
// when the column is exactly one cell wide and the input doesn't
// already fit.
func Ellipsize(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if cellWidth(s) <= w {
		return s
	}
	if w == 1 {
		return EllipsizeSuffix
	}
	return Truncate(s, w-1) + EllipsizeSuffix
}

// cellWidth is a thin wrapper over the package's existing
// width-aware accounting so Ellipsize can be tested without
// importing lipgloss directly. Inlines to lipgloss.Width but
// keeps the allocator file self-contained on its public API.
func cellWidth(s string) int {
	used := 0
	for _, r := range s {
		used += runeWidth(r)
	}
	return used
}
