// SPDX-License-Identifier: Apache-2.0

// Package stateformat carries the app-global toggle between the
// full and compact renderings of the alerts page's state breakdown
// (the STATE column tally over active / suppressed / unprocessed).
// It mirrors timerender.Format's role for the time toggle: a tiny
// enum the App owns and broadcasts so the alerts list (L1) and the
// group-detail instance list (L2) agree on density. Full is the
// default — the explicit `9 active · 3 suppressed` words; Compact is
// the dense `9ac 3su` form.
package stateformat

// Format selects the state-breakdown rendering density. The zero
// value is Full so a page constructed before any toggle opens in the
// legible default.
type Format int

const (
	// Full renders explicit words: `9 active · 3 suppressed`.
	Full Format = iota
	// Compact renders count + two-letter abbreviation: `9ac 3su`.
	Compact
)

// String is the human label surfaced in the toggle flash
// ("state: compact"). Mirrors timerender.Format.String().
func (f Format) String() string {
	if f == Compact {
		return "compact"
	}
	return "full"
}
