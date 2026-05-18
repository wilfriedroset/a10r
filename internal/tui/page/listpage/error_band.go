// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

// ErrorBand returns the one-line message rendered above the table
// when at least one in-scope tenant is reporting a transport
// error. Empty when every in-scope tenant is healthy (or
// unpolled) so the renderer can short-circuit.
//
// Single-tenant scope: surfaces that tenant's detail verbatim.
// Multi-tenant scope with one offender: prefixes the tenant
// name. Multi-tenant scope with several offenders: collapses to
// a count plus the first detail (alphabetically) so the band
// stays one line.
func (b *Base) ErrorBand() string {
	type entry struct {
		tenant string
		detail string
	}
	var bad []entry
	for tenant, h := range b.BackendHealth {
		if h.Detail == "" {
			continue
		}
		if !b.ScopeIncludes(tenant) {
			continue
		}
		bad = append(bad, entry{tenant: tenant, detail: h.Detail})
	}
	if len(bad) == 0 {
		return ""
	}
	// Sort by tenant for deterministic output across runs (map
	// iteration order is unspecified).
	sort.Slice(bad, func(i, j int) bool { return bad[i].tenant < bad[j].tenant })
	if len(bad) == 1 {
		// Single offender: tenant prefix only useful when scope
		// covers >1 tenant (avoids "prod: …" noise on a
		// single-tenant view).
		if b.Scope == scopeAll || strings.Contains(b.Scope, ",") {
			return bad[0].tenant + ": " + bad[0].detail
		}
		return bad[0].detail
	}
	return fmt.Sprintf("%d backends erroring; %s: %s", len(bad), bad[0].tenant, bad[0].detail)
}

// RenderErrorBand returns the styled one-line band the page View
// prepends, or "" when ErrorBand is empty. fg is the foreground
// the page wants to paint the message in — usually the severity-
// critical foreground from the page's styles. Wider than width is
// clipped with format.SGRTruncate so a long upstream error never
// breaks the layout. Background is intentionally not painted: the
// chrome stays on the terminal default per the rendering memory.
func (b *Base) RenderErrorBand(width int, fg color.Color) string {
	msg := b.ErrorBand()
	if msg == "" {
		return ""
	}
	full := "! " + msg
	if lipgloss.Width(full) > width {
		full = format.SGRTruncate(full, width)
	}
	return lipgloss.NewStyle().Foreground(fg).Render(full)
}
