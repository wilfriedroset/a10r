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
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	labelNow         = "now"
	labelRetryingNow = "retrying now"
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
// time-format toggle is set to Absolute. ISO-style local time —
// year-month-day HH:MM:SS, no timezone marker so the column stays
// narrow enough for the widened AGE / ENDS / STARTS budgets.
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
		return labelRetryingNow
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

// parseUnit is one entry in the units table. Each row carries the
// rank (higher = larger unit) so the ordering check and the
// largest-first re-render share a single source of truth.
type parseUnit struct {
	letter byte
	rank   int
	d      time.Duration
}

// parseUnits lists the Duration shorthand units largest-first so
// the suggested rewrite for an out-of-order input is composed by
// re-emitting parsed terms in this order.
var parseUnits = []parseUnit{
	{'w', 5, 7 * 24 * time.Hour},
	{'d', 4, 24 * time.Hour},
	{'h', 3, time.Hour},
	{'m', 2, time.Minute},
	{'s', 1, time.Second},
}

// Parse decodes a Duration shorthand string (e.g. `7d`, `1w2d3h`,
// `1.5h`) into a time.Duration. The grammar accepts one or more
// `<float><unit>` terms with units `s m h d w` largest-first, each
// unit at most once, with optional whitespace between terms. The
// returned duration is rounded to the nearest second so the
// silence backend never sees sub-second junk. See ADR 0034 and
// CONTEXT.md "Duration shorthand" for the canonical grammar.
//
// Errors are tailored: capital `M`/`W`/`Y` get a named rejection
// (a single-letter `m` for "month" is the documented footgun);
// an out-of-order input gets a "rewrite as <largest-first>" hint;
// a unit-less digit gets "missing unit". Callers wrap the message
// with whatever field prefix is appropriate (the silence form
// prepends `ends: `).
func Parse(in string) (time.Duration, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	var terms []parseTerm
	pos := 0
	for pos < len(s) {
		if isSpace(s[pos]) {
			pos++
			continue
		}
		t, next, err := parseOneTerm(s, pos, terms)
		if err != nil {
			return 0, err
		}
		terms = append(terms, t)
		pos = next
	}
	if len(terms) == 0 {
		return 0, garbageError()
	}
	if err := checkOrder(terms); err != nil {
		return 0, err
	}
	var total time.Duration
	for _, t := range terms {
		total += t.ns
	}
	return total.Round(time.Second), nil
}

// parseTerm is one `<float><unit>` pair the term parser produces.
// `ratio` survives alongside `ns` so the out-of-order rewrite hint
// can re-emit the term in its source form (integer or decimal).
type parseTerm struct {
	unit  parseUnit
	ns    time.Duration
	ratio float64
}

// parseOneTerm extracts the next `<float><unit>` term from s
// starting at pos. Returns the parsed term, the position the outer
// loop should continue from, and any tailored error. The seen
// slice is consulted so a repeated unit fails before the float
// parses.
func parseOneTerm(s string, pos int, seen []parseTerm) (parseTerm, int, error) {
	numStr, alphaRun, next, err := splitTerm(s, pos)
	if err != nil {
		return parseTerm{}, 0, err
	}
	unit, err := resolveUnit(numStr, alphaRun)
	if err != nil {
		return parseTerm{}, 0, err
	}
	for _, t := range seen {
		if t.unit.letter == unit.letter {
			return parseTerm{}, 0, fmt.Errorf("unit %q appears more than once", string(unit.letter))
		}
	}
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return parseTerm{}, 0, fmt.Errorf("expected number before unit %q", string(unit.letter))
	}
	return parseTerm{
		unit:  unit,
		ns:    time.Duration(val * float64(unit.d)),
		ratio: val,
	}, next, nil
}

// splitTerm walks the next `<digits-and-dot><alpha>` slice out of
// s starting at pos. Returns the numeric prefix (possibly empty),
// the alphabetic run, the new position, and any structural error
// (no-unit-after-digits, garbage with no recognisable shape).
func splitTerm(s string, pos int) (numStr, alphaRun string, next int, err error) {
	numStart := pos
	for pos < len(s) && (isDigit(s[pos]) || s[pos] == '.') {
		pos++
	}
	numStr = s[numStart:pos]
	if pos >= len(s) {
		if numStr == "" {
			return "", "", 0, garbageError()
		}
		return "", "", 0, errors.New("missing unit; use s m h d w")
	}
	alphaStart := pos
	for pos < len(s) && isAlpha(s[pos]) {
		pos++
	}
	alphaRun = s[alphaStart:pos]
	if alphaRun == "" {
		return "", "", 0, garbageError()
	}
	return numStr, alphaRun, pos, nil
}

// resolveUnit maps the alphabetic run to a unit, dispatching the
// tailored capital / unknown-unit / missing-number errors. The
// number prefix is read here because the missing-number branch
// shares its remediation message with the unknown-unit branch
// only for short alphabetic runs (`dx`, `xh`); longer runs read
// as English garbage and short-circuit through garbageError.
func resolveUnit(numStr, alphaRun string) (parseUnit, error) {
	if msg, ok := capitalErr(alphaRun[0]); ok {
		return parseUnit{}, errors.New(msg)
	}
	if numStr == "" {
		if len(alphaRun) > 2 {
			return parseUnit{}, garbageError()
		}
		return parseUnit{}, fmt.Errorf("expected number before unit %q", string(alphaRun[0]))
	}
	if len(alphaRun) != 1 {
		return parseUnit{}, fmt.Errorf("unknown unit %q (use s m h d w)", alphaRun)
	}
	unit, ok := lookupUnit(alphaRun[0])
	if !ok {
		return parseUnit{}, fmt.Errorf("unknown unit %q (use s m h d w)", alphaRun)
	}
	return unit, nil
}

// checkOrder reports an out-of-order term sequence by composing the
// suggested rewrite — terms re-sorted largest-first and rendered
// back to shorthand. Repeated-unit inputs are caught earlier in
// parseOneTerm, so any non-strictly-decreasing rank here is order
// alone.
func checkOrder(terms []parseTerm) error {
	for i := 1; i < len(terms); i++ {
		if terms[i].unit.rank < terms[i-1].unit.rank {
			continue
		}
		sorted := append([]parseTerm(nil), terms...)
		sort.SliceStable(sorted, func(a, b int) bool {
			return sorted[a].unit.rank > sorted[b].unit.rank
		})
		var b strings.Builder
		for _, t := range sorted {
			b.WriteString(formatRatio(t.ratio))
			b.WriteByte(t.unit.letter)
		}
		return fmt.Errorf("units must be ordered largest-first; rewrite as %s", b.String())
	}
	return nil
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

// formatRatio renders a parsed term's float coefficient back into
// its source form for the out-of-order rewrite hint. Integer values
// render without a trailing `.0`; fractions render with the minimum
// decimals strconv can manage.
func formatRatio(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// capitalErr names the three capitals operators most commonly
// confuse with units. `M` is the documented footgun (cron / human
// English "month" vs. the `m`-as-minute grammar); `W` and `Y` are
// covered by symmetry so the rejection is uniform across the
// "obvious" capital attempts.
func capitalErr(b byte) (string, bool) {
	switch b {
	case 'M':
		return "M is not a unit; m means minute (1m=60s); use 30d if you meant ~month", true
	case 'W':
		return "W is not a unit; w means week (1w=7d)", true
	case 'Y':
		return "Y is not a unit; years are not supported; use 365d", true
	}
	return "", false
}

func lookupUnit(b byte) (parseUnit, bool) {
	for _, u := range parseUnits {
		if u.letter == b {
			return u, true
		}
	}
	return parseUnit{}, false
}

func garbageError() error {
	return errors.New("not a duration (try 7d, 2h30m, 1w2d)")
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
		return labelNow
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
//nolint:gosmopolitan // local time is the explicit operator-facing choice
func absolute(ts time.Time) string {
	return ts.Local().Format(absoluteFormat)
}
