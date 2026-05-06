// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"errors"
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"
)

// errEmptyColor is the sentinel for an empty color string; callers
// branch on it via errors.Is when distinguishing "user forgot the
// field entirely" from "user wrote something we couldn't parse".
var errEmptyColor = errors.New("empty color value")

// hexColorRE matches the 6-digit `#rrggbb` form. 3-digit and 8-digit
// (alpha) forms are deliberately not accepted: terminals can't render
// alpha, and the 3-digit shorthand isn't used by any k9s skin we
// ship or import.
var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// defaultColor is the sentinel returned for the literal `default`
// keyword. The styles compiler treats it as "skip the lipgloss
// Foreground/Background call entirely so the terminal's native
// color shows through" — distinct from any valid RGB value.
//
// We use a private named type rather than a magic color.RGBA so a
// future styles consumer can switch on the type.
type terminalDefault struct{}

func (terminalDefault) RGBA() (r, g, b, a uint32) { return 0, 0, 0, 0 }

var defaultColor color.Color = terminalDefault{}

// isDefaultColor reports whether c is the terminal-default sentinel.
// Compile sites use it to decide whether to call lipgloss's
// Foreground/Background setter or leave the slot unset.
func isDefaultColor(c color.Color) bool {
	_, ok := c.(terminalDefault)
	return ok
}

// parseColor accepts the three forms documented for skin files:
//
//   - `#rrggbb` 6-digit hex
//   - `default` — terminal-native (no styling)
//   - SVG/CSS named colors (case-insensitive, "grey" alias accepted)
//
// Numeric ANSI palette values (`9`, `21`, …) are deliberately
// rejected: no public k9s skin we have to support uses them, and
// accepting them would require either a separate parse path or
// gambling that an unknown-name lookup happens to be a number.
func parseColor(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errEmptyColor
	}
	if s == "default" {
		return defaultColor, nil
	}
	if hexColorRE.MatchString(s) {
		// strconv path is the cheapest way to get a color.RGBA
		// out of `#rrggbb`; we trim the leading '#' and parse as
		// a 24-bit hex.
		v, err := strconv.ParseUint(s[1:], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid hex color %q: %w", s, err)
		}
		return rgb24(uint32(v)), nil
	}
	// SVG name lookup is case-insensitive; the table is keyed on
	// the lowercase form to match how every k9s skin file writes
	// these (k9s itself canonicalises to lowercase).
	if hex, ok := svgColors[strings.ToLower(s)]; ok {
		return rgb24(hex), nil
	}
	return nil, fmt.Errorf("unknown color %q (expected `#rrggbb`, `default`, or an SVG color name)", s)
}

// rgb24 unpacks a 24-bit `0xRRGGBB` value into a color.RGBA. The
// explicit `& 0xFF` masking is for gosec/G115 — without it the
// uint32→uint8 narrowing reads as a potential overflow even though
// each shift is bounded.
func rgb24(v uint32) color.RGBA {
	return color.RGBA{
		R: uint8((v >> 16) & 0xFF),
		G: uint8((v >> 8) & 0xFF),
		B: uint8(v & 0xFF),
		A: 0xFF,
	}
}
