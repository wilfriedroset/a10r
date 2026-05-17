// SPDX-License-Identifier: Apache-2.0

package listpage

import "github.com/wilfriedroset/a10r/internal/tui/page/cursor"

// ReconcileScroll re-aligns TopRow with Cursor for the cached
// BodyHeight. No-op when BodyHeight is unset (cold start before
// the first View has measured the body). Wraps the pure
// cursor.ReconcileScroll so pages do not touch TopRow directly.
func (b *Base) ReconcileScroll(itemCount int) {
	if b.BodyHeight <= 0 {
		return
	}
	b.TopRow = cursor.ReconcileScroll(b.Cursor, b.TopRow, b.BodyHeight, itemCount)
}
