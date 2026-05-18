// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/header"
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
//
// All layouts get a `— retrying in <unit>` suffix; the same
// alphabetically-first entry that sources the detail also
// sources the NextAt clock the suffix counts down from.
// Past-due (a tick is in flight) renders `— retrying now`. See
// CONTEXT.md "Next attempt" and ADR-0014.
func (b *Base) ErrorBand(now time.Time) string {
	type entry struct {
		tenant string
		detail string
		nextAt time.Time
	}
	var bad []entry
	for tenant, h := range b.BackendHealth {
		if h.Detail == "" {
			continue
		}
		if !b.ScopeIncludes(tenant) {
			continue
		}
		bad = append(bad, entry{tenant: tenant, detail: h.Detail, nextAt: h.NextAt})
	}
	if len(bad) == 0 {
		return ""
	}
	// Sort by tenant for deterministic output across runs (map
	// iteration order is unspecified).
	sort.Slice(bad, func(i, j int) bool { return bad[i].tenant < bad[j].tenant })
	head := bad[0]
	suffix := " — " + nextAttemptLabel(now, head.nextAt)
	if len(bad) == 1 {
		// Single offender: tenant prefix only useful when scope
		// covers >1 tenant (avoids "prod: …" noise on a
		// single-tenant view).
		if b.Scope == scopeAll || strings.Contains(b.Scope, ",") {
			return head.tenant + ": " + head.detail + suffix
		}
		return head.detail + suffix
	}
	return fmt.Sprintf("%d backends erroring; %s: %s%s", len(bad), head.tenant, head.detail, suffix)
}

// RenderErrorBand returns the styled one-line band the page View
// prepends, or "" when ErrorBand is empty. fg is the foreground
// the page wants to paint the message in — usually the severity-
// critical foreground from the page's styles. Wider than width is
// clipped with format.SGRTruncate so a long upstream error never
// breaks the layout. Background is intentionally not painted: the
// chrome stays on the terminal default per the rendering memory.
func (b *Base) RenderErrorBand(now time.Time, width int, fg color.Color) string {
	msg := b.ErrorBand(now)
	if msg == "" {
		return ""
	}
	full := "! " + msg
	if lipgloss.Width(full) > width {
		full = format.SGRTruncate(full, width)
	}
	return lipgloss.NewStyle().Foreground(fg).Render(full)
}

// nextAttemptLabel renders the "Next attempt" relative-time suffix
// per CONTEXT.md, reusing the s/m/h/d ladder from
// header.FormatDuration so the vocabulary stays in lockstep with
// every other compact relative-time site. Past-due (negative
// delta, zero NextAt, or sub-second future) returns the literal
// `retrying now` (active voice); a future NextAt prefixes the
// formatted duration with `retrying in `.
func nextAttemptLabel(now, nextAt time.Time) string {
	d := nextAt.Sub(now)
	if d < time.Second {
		return "retrying now"
	}
	return "retrying in " + header.FormatDuration(d)
}
