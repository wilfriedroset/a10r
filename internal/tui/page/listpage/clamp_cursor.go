// SPDX-License-Identifier: Apache-2.0

package listpage

import "github.com/wilfriedroset/a10r/internal/tui/page/cursor"

// ClampCursor bounds Cursor into the visible range. Wraps
// cursor.Clamp so pages do not reach into the field directly and
// every list page agrees on what "clamp to view" means.
func (b *Base) ClampCursor(itemCount int) {
	b.Cursor = cursor.Clamp(b.Cursor, itemCount)
}
