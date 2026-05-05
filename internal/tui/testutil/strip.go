// SPDX-License-Identifier: Apache-2.0

// Package testutil holds helpers shared across TUI tests so the
// per-package _test.go files don't reimplement the same scaffolding.
//
// Helpers here must be cheap, side-effect-free, and obviously
// correct — they're called from many test packages and a regression
// would silently weaken assertions across the suite.
package testutil

import "strings"

// StripStyle drops ANSI SGR sequences (\x1b[…m) from s so test
// assertions can do plain substring matches against rendered TUI
// output without coupling to the active palette. Non-styled input
// passes through unchanged.
func StripStyle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
			// drop the byte; we're inside an SGR escape
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
