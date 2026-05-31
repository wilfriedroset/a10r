// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"image/color"
	"time"
)

// ListFrame is the per-render input to RenderListFrame. Each list page
// supplies its current row count, the error-band inputs, and the three
// body renderers; Header and Rows run only on the populated path.
type ListFrame struct {
	Width, Height int
	Now           time.Time
	CritColor     color.Color
	Count         int
	EmptyState    func() string
	Header        func(width int) string
	Rows          func(width, maxRows int) string
}

// RenderListFrame renders the list-page shell the alerts, silences and
// groups pages otherwise copy verbatim: an optional error band stacked
// above either the empty-state body (in a bg-less Pane) or the
// header+rows frame (in Wrap). It owns the band-line bookkeeping and
// the SetViewport call so each page's View collapses to wiring.
func (b *Base) RenderListFrame(f ListFrame) string {
	if f.Width <= 0 || f.Height <= 0 {
		return ""
	}
	band := b.RenderErrorBand(f.Now, f.Width, f.CritColor)
	bandLines := 0
	if band != "" {
		bandLines = 1
	}
	b.SetViewport(f.Height-1-bandLines, f.Count)

	if f.Count == 0 {
		return Pane(f.Width, f.Height, stackBand(band, f.EmptyState()))
	}
	body := f.Header(f.Width) + "\n" + f.Rows(f.Width, f.Height-1-bandLines)
	return Wrap(f.Width, stackBand(band, body))
}

// stackBand prepends a non-empty error band above body.
func stackBand(band, body string) string {
	if band == "" {
		return body
	}
	return band + "\n" + body
}
