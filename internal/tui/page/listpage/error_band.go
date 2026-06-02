// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// ErrorBand returns the one-line message above the table when an
// in-scope tenant reports a transport error, "" when all are healthy
// (or unpolled). Single-tenant scope shows the detail verbatim;
// multi-tenant with one offender prefixes the name; several offenders
// collapse to a count plus the alphabetically-first detail so the
// band stays one line. Every layout gets a `— retrying …` suffix
// counting down from that same entry's NextAt. See CONTEXT.md "Next
// attempt" and ADR-0014.
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
	// Deterministic output: map iteration order is unspecified.
	sort.Slice(bad, func(i, j int) bool { return bad[i].tenant < bad[j].tenant })
	head := bad[0]
	suffix := " — " + timerender.NextAttempt(now, head.nextAt)
	if len(bad) == 1 {
		// Tenant prefix only when scope spans >1 tenant — avoids
		// "prod: …" noise on a single-tenant view.
		if b.Scope == ScopeAll || strings.Contains(b.Scope, ",") {
			return head.tenant + ": " + head.detail + suffix
		}
		return head.detail + suffix
	}
	return fmt.Sprintf("%d backends erroring; %s: %s%s", len(bad), head.tenant, head.detail, suffix)
}

// RenderErrorBand returns the styled band the View prepends, "" when
// ErrorBand is empty. Truncated to width so a long upstream error
// never breaks the layout. Background is left unpainted so the chrome
// stays on the terminal default (fg-only rendering memory).
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
