// SPDX-License-Identifier: Apache-2.0

// Package timerender owns the four CONTEXT.md time-rendering
// vocabularies — relative time, absolute time, remaining, next
// attempt — plus a Duration primitive shared by status-page uptime
// and the next-attempt single-unit ladder. Pure functions, no
// hidden state: callers branch on the page-level Format toggle by
// passing it to Display.
//
// See docs/adr/0015-timerender-vocabulary.md for the design and
// CONTEXT.md "Time rendering" for the vocabulary contracts.
package timerender

import (
	"fmt"
	"time"
)

// Format selects between the relative and absolute rendering modes
// Display branches on. The zero value is Relative — every list page
// boots in relative mode until the user presses `t`.
type Format int

const (
	// Relative renders timestamps as "5m ago" / "in 2h" — the
	// single-unit compact form used in table columns.
	Relative Format = iota
	// Absolute renders timestamps as the local-zone ISO layout
	// `YYYY-MM-DD HH:MM:SS`, toggled app-globally with `t`.
	Absolute
)

// String returns a short identifier for the format suitable for
// header content ("relative" / "absolute") and the flash on toggle.
func (f Format) String() string {
	if f == Absolute {
		return "absolute"
	}
	return "relative"
}

// absoluteFormat is the layout pages use when the app-global
// time-format toggle is set to Absolute. ISO-style local time per
// Q7.4: year-month-day HH:MM:SS, no timezone marker so the column
// stays narrow enough for the widened AGE / ENDS / STARTS budgets.
const absoluteFormat = "2006-01-02 15:04:05"

// Display renders ts according to the supplied format. Returns ""
// when ts is zero in either mode so callers can route through one
// helper without an outer nil guard.
func Display(f Format, now, ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	if f == Absolute {
		return absolute(ts)
	}
	return relative(now, ts)
}

// Remaining renders the forward-looking duration between now and
// future as mixed-unit prose ("2h13m", "4d") for narrative fields
// such as the alert-detail "expires in" line. The vocabulary is
// strictly forward-looking per CONTEXT.md — a non-positive delta
// returns "" and the caller owns any past-case label.
//
// Granularity matches what an operator wants to see at a glance:
// days when >=1d, hours+minutes when >=1h, minutes when >=1m,
// seconds otherwise. No mixed h/m/s rendering.
func Remaining(now, future time.Time) string {
	d := future.Sub(now)
	if d <= 0 {
		return ""
	}
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if d >= time.Hour {
		hours := int(d / time.Hour)
		mins := int((d - time.Duration(hours)*time.Hour) / time.Minute)
		if mins == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, mins)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

// NextAttempt renders the failure-mode tick clock as "retrying in
// Xs/m/h/d" using the single-unit Duration ladder. Past-due (zero
// deadline, negative delta, or sub-second future) renders "retrying
// now" because a sub-second deadline means the tick is already late,
// not that the poller has nothing to render.
func NextAttempt(now, deadline time.Time) string {
	d := deadline.Sub(now)
	if d < time.Second {
		return "retrying now"
	}
	return "retrying in " + Duration(d)
}

// Duration renders d as a compact single-unit string using the
// s / m / h / d ladder — no "ago" / "in" suffix because the caller
// already labels what the value represents (e.g. "uptime 5d").
// Sub-second durations collapse to "0s" so a freshly-booted backend
// never produces a noisy header. Negative values are taken as |d|.
func Duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// relative renders the duration between now and ts as a compact
// relative-time string. Past: "X ago" (e.g. "2h ago"). Future:
// "in X" (e.g. "in 2h"). |Δt|<1s on either side renders "now" to
// absorb polling jitter. Single-unit ladder: s, m, h, d.
func relative(now, ts time.Time) string {
	d := ts.Sub(now)
	abs := d
	if abs < 0 {
		abs = -abs
	}
	if abs < time.Second {
		return "now"
	}
	var unit string
	switch {
	case abs < time.Minute:
		unit = fmt.Sprintf("%ds", int(abs.Seconds()))
	case abs < time.Hour:
		unit = fmt.Sprintf("%dm", int(abs.Minutes()))
	case abs < 24*time.Hour:
		unit = fmt.Sprintf("%dh", int(abs.Hours()))
	default:
		unit = fmt.Sprintf("%dd", int(abs/(24*time.Hour)))
	}
	if d < 0 {
		return unit + " ago"
	}
	return "in " + unit
}

// absolute renders ts in the local-zone ISO layout.
//
//nolint:gosmopolitan // local time is the explicit operator-facing choice per Q7.4
func absolute(ts time.Time) string {
	return ts.Local().Format(absoluteFormat)
}
