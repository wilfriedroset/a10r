// SPDX-License-Identifier: Apache-2.0

package detailpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

// HandleScrollKey consumes the seven scroll motions (j/k, G,
// Ctrl+D/U/F/B) and reports whether the key was claimed. Unknown keys
// return handled=false.
//
// The G "1 << 30" sentinel relies on the next frame's ReconcileScroll
// to clamp it against the actual body length.
func (b *Base) HandleScrollKey(key string) (handled bool) {
	switch key {
	case "j", "down":
		b.Scroll++
	case "k", "up":
		if b.Scroll > 0 {
			b.Scroll--
		}
	case "ctrl+d":
		b.Scroll += cursor.HalfPageStep(b.BodyHeight)
	case "ctrl+u":
		b.Scroll = max(b.Scroll-cursor.HalfPageStep(b.BodyHeight), 0)
	case "ctrl+f":
		b.Scroll += cursor.FullPageStep(b.BodyHeight)
	case "ctrl+b":
		b.Scroll = max(b.Scroll-cursor.FullPageStep(b.BodyHeight), 0)
	case "G":
		b.Scroll = 1 << 30
	default:
		return false
	}
	return true
}

// ReconcileScroll clamps Scroll into [0, max(totalLines-height, 0)] so
// the visible window stays within the body.
func (b *Base) ReconcileScroll(totalLines, height int) {
	if b.Scroll < 0 {
		b.Scroll = 0
		return
	}
	maxScroll := max(totalLines-height, 0)
	if b.Scroll > maxScroll {
		b.Scroll = maxScroll
	}
}
