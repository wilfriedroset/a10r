// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"testing"
)

func TestParseColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		// when wantDefault is true, the parser must return the
		// terminal-default sentinel; rgb is ignored.
		wantDefault bool
		// rgb checked when not wantDefault and not wantErr.
		// 0xRRGGBB packed.
		rgb uint32
	}{
		{name: "empty rejected", input: "", wantErr: true},

		{name: "default keyword", input: "default", wantDefault: true},

		{name: "lower hex", input: "#1e1e2e", rgb: 0x1E1E2E},
		{name: "upper hex", input: "#FFFFFF", rgb: 0xFFFFFF},
		{name: "mixed hex", input: "#AbCdEf", rgb: 0xABCDEF},

		{name: "named dodgerblue", input: "dodgerblue", rgb: 0x1E90FF},
		{name: "named aqua", input: "aqua", rgb: 0x00FFFF},
		{name: "named mediumpurple", input: "mediumpurple", rgb: 0x9370DB},
		{name: "named lightskyblue", input: "lightskyblue", rgb: 0x87CEFA},
		{name: "named orangered", input: "orangered", rgb: 0xFF4500},
		{name: "named greenyellow", input: "greenyellow", rgb: 0xADFF2F},
		{name: "named lightslategray", input: "lightslategray", rgb: 0x778899},
		// `grey` aliases survive — needed because some skins use
		// the British spelling.
		{name: "named grey alias", input: "grey", rgb: 0x808080},

		{name: "case-insensitive name", input: "DodgerBlue", rgb: 0x1E90FF},
		{name: "trim whitespace", input: "  red  ", rgb: 0xFF0000},

		{name: "3-digit hex rejected", input: "#abc", wantErr: true},
		{name: "8-digit hex rejected (alpha)", input: "#aabbccdd", wantErr: true},
		{name: "non-hex chars rejected", input: "#zzzzzz", wantErr: true},
		{name: "unknown name rejected", input: "notacolor", wantErr: true},
		{name: "numeric ANSI rejected", input: "9", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseColor(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseColor(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseColor(%q) unexpected error: %v", tt.input, err)
			}
			if tt.wantDefault {
				if got != defaultColor {
					t.Fatalf("parseColor(%q) = %v, want defaultColor sentinel", tt.input, got)
				}
				return
			}
			r, g, b, _ := got.RGBA()
			// RGBA returns 16-bit per channel; squash to 8-bit.
			gotRGB := (r>>8)<<16 | (g>>8)<<8 | b>>8
			if gotRGB != tt.rgb {
				t.Fatalf("parseColor(%q) = #%06x, want #%06x", tt.input, gotRGB, tt.rgb)
			}
		})
	}
}

func TestDefaultColorIsTerminalNative(t *testing.T) {
	t.Parallel()

	// The `default` sentinel must be distinguishable from a real
	// black hex value, otherwise the styles compiler can't tell
	// the difference between "user wants terminal default" and
	// "user wants #000000".
	parsed, err := parseColor("default")
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	black, err := parseColor("#000000")
	if err != nil {
		t.Fatalf("#000000: %v", err)
	}
	if parsed == black {
		t.Fatal("defaultColor must not equal parsed #000000")
	}
	// And it must *not* be a normal color.Color the lipgloss layer
	// would render as a real color.
	if !isDefaultColor(parsed) {
		t.Fatal("isDefaultColor(defaultColor) must be true")
	}
	if isDefaultColor(black) {
		t.Fatal("isDefaultColor(#000000) must be false")
	}
	// Sanity: a color from the SVG name table is also not default.
	dodger, _ := parseColor("dodgerblue")
	if isDefaultColor(dodger) {
		t.Fatal("isDefaultColor(named color) must be false")
	}
}
