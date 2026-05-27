// SPDX-License-Identifier: Apache-2.0

// Package testutil holds helpers shared across TUI tests so the
// per-package _test.go files don't reimplement the same scaffolding.
//
// Helpers here must be cheap, side-effect-free, and obviously
// correct — they're called from many test packages and a regression
// would silently weaken assertions across the suite.
package testutil

import (
	"regexp"
	"strconv"
	"strings"
)

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

// HasBackground reports whether s contains an SGR sequence that sets
// a background colour: named (40-47 / 100-107) or extended (48;5;n /
// 48;2;r;g;b). It parses each sequence's parameters rather than
// substring-matching, so it returns false for foreground-only output
// even when fg and bg would share one combined SGR (\x1b[38;…;48;…m)
// — the case a plain "\x1b[48" check misses — and never mistakes a
// foreground RGB component that happens to equal a bg code.
func HasBackground(s string) bool {
	sgr := regexp.MustCompile("\x1b\\[([0-9;]*)m")
	for _, m := range sgr.FindAllStringSubmatch(s, -1) {
		if sgrParamsSetBackground(strings.Split(m[1], ";")) {
			return true
		}
	}
	return false
}

// sgrParamsSetBackground reports whether the ;-split parameters of one
// SGR sequence set a background colour. Extended foreground colours
// (38;5;n / 38;2;r;g;b) are skipped so an RGB component equal to a bg
// code isn't misread as a background.
func sgrParamsSetBackground(params []string) bool {
	for i := 0; i < len(params); i++ {
		switch params[i] {
		case "48":
			return true
		case "38":
			i += extendedColorArgs(params, i)
		default:
			if isNamedBackground(params[i]) {
				return true
			}
		}
	}
	return false
}

// extendedColorArgs returns how many parameters after the introducer
// at index i belong to an extended-colour spec: 2 for 5;n (256) and
// 4 for 2;r;g;b (truecolor), 0 otherwise.
func extendedColorArgs(params []string, i int) int {
	if i+1 >= len(params) {
		return 0
	}
	switch params[i+1] {
	case "5":
		return 2
	case "2":
		return 4
	}
	return 0
}

func isNamedBackground(param string) bool {
	n, err := strconv.Atoi(param)
	return err == nil && ((n >= 40 && n <= 47) || (n >= 100 && n <= 107))
}
