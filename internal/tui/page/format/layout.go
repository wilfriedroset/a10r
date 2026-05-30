// SPDX-License-Identifier: Apache-2.0

package format

// FlexUnbounded is the Content sentinel for an uncapped flex column.
// Using a finite value avoids edge cases in integer arithmetic while
// exceeding any real terminal width.
const FlexUnbounded = 1 << 16

// RowPrefixCols is the width of the leading "▸ ✓ " prefix every
// table row reserves. Header renderers subtract this to align titles.
const RowPrefixCols = 4
