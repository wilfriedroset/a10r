// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Fuzz frame layout. Each frame is exactly FuzzFrameSize bytes;
// trailing bytes that don't make a full frame are discarded by
// DecodeFuzzMsgs. The numeric kind values are part of the
// on-disk fuzz corpus encoding — changing them invalidates every
// checked-in repro under testdata/fuzz/.
//
// frame[0] %% 16 selects the message kind:
//
//	0..13 → KeyPressMsg, parameterised by frame[1] / frame[2].
//	14    → WindowSizeMsg, frame[1] %% 64 * 4 = width, frame[2]
//	        %% 64 * 4 = height (covers 0..252 cells).
//	15    → idle frame (no msg emitted).
//
// For the key kind, frame[1] picks the key:
//
//	0 → Enter, 1 → Escape, 2 → Tab, 3 → Backspace, 4 → Space.
//	≥5 → printable ASCII; rune = 0x20 + (frame[1] - 5) %% 95.
//
// frame[2] %% 4 is the modifier mask: 0 none, 1 Ctrl, 2 Shift,
// 3 Alt. The encoding is fully invertible for any printable rune
// — see TestFuzzFrameKey_RoundTripsToPrintableBranch.
const (
	FuzzFrameSize    = 3
	fuzzKindModulus  = 16
	fuzzKindResize   = 14
	fuzzKindNoop     = 15
	fuzzKeyEnter     = 0
	fuzzKeyEscape    = 1
	fuzzKeyTab       = 2
	fuzzKeyBackspace = 3
	fuzzKeySpace     = 4
	fuzzKeyPrintLo   = 5
	// Printable ASCII window: 0x20..0x7E (95 runes; 0x7F DEL is
	// intentionally excluded — keeps the corpus off a control byte
	// few real users ever produce).
	fuzzPrintableLo  = 0x20
	fuzzPrintableLen = 95
)

// DecodeFuzzMsgs parses a fuzz byte stream into a sequence of
// tea.Msg per the encoding documented above.
func DecodeFuzzMsgs(in []byte) []tea.Msg {
	out := make([]tea.Msg, 0, len(in)/FuzzFrameSize)
	for i := 0; i+FuzzFrameSize <= len(in); i += FuzzFrameSize {
		switch in[i] % fuzzKindModulus {
		case fuzzKindResize:
			w := int(in[i+1]%64) * 4
			h := int(in[i+2]%64) * 4
			out = append(out, tea.WindowSizeMsg{Width: w, Height: h})
		case fuzzKindNoop:
			// idle frame
		default:
			out = append(out, decodeFuzzKey(in[i+1], in[i+2]))
		}
	}
	return out
}

// decodeFuzzKey turns two payload bytes into a KeyPressMsg.
func decodeFuzzKey(rune8, mod8 byte) tea.KeyPressMsg {
	var code rune
	var text string
	switch rune8 {
	case fuzzKeyEnter:
		code = tea.KeyEnter
	case fuzzKeyEscape:
		code = tea.KeyEscape
	case fuzzKeyTab:
		code = tea.KeyTab
	case fuzzKeyBackspace:
		code = tea.KeyBackspace
	case fuzzKeySpace:
		code = tea.KeySpace
		text = " "
	default:
		code = rune(fuzzPrintableLo + int(rune8-fuzzKeyPrintLo)%fuzzPrintableLen)
		text = string(code)
	}
	var modMask tea.KeyMod
	switch mod8 % 4 {
	case 1:
		modMask = tea.ModCtrl
	case 2:
		modMask = tea.ModShift
	case 3:
		modMask = tea.ModAlt
	}
	return tea.KeyPressMsg{Code: code, Text: text, Mod: modMask}
}

// FuzzSeed assembles a sequence of frames into the byte stream
// f.Add expects. Used by per-target seed corpora.
func FuzzSeed(frames ...[FuzzFrameSize]byte) []byte {
	out := make([]byte, 0, len(frames)*FuzzFrameSize)
	for _, fr := range frames {
		out = append(out, fr[:]...)
	}
	return out
}

// FuzzFrameKey encodes a printable ASCII rune (0x20..0x7E) as a
// key frame. Panics on a non-printable rune so a future seed
// using a special key surfaces immediately rather than aliasing.
// Use FuzzFrameKeyCode for Enter/Escape/Tab/Backspace/Space.
func FuzzFrameKey(r rune) [FuzzFrameSize]byte {
	if r < fuzzPrintableLo || r >= fuzzPrintableLo+fuzzPrintableLen {
		panic(fmt.Sprintf("FuzzFrameKey: %q outside printable ASCII; use FuzzFrameKeyCode for special keys", r))
	}
	rune8 := byte(int(r-fuzzPrintableLo) + fuzzKeyPrintLo)
	return [FuzzFrameSize]byte{0, rune8, 0}
}

// FuzzFrameKeyCode encodes one of the named special keys
// (Enter/Escape/Tab/Backspace/Space). Panics on an unsupported
// code so a future tea.Key* addition surfaces immediately rather
// than aliasing to Enter.
func FuzzFrameKeyCode(code rune) [FuzzFrameSize]byte {
	var rune8 byte
	switch code {
	case tea.KeyEnter:
		rune8 = fuzzKeyEnter
	case tea.KeyEscape:
		rune8 = fuzzKeyEscape
	case tea.KeyTab:
		rune8 = fuzzKeyTab
	case tea.KeyBackspace:
		rune8 = fuzzKeyBackspace
	case tea.KeySpace:
		rune8 = fuzzKeySpace
	default:
		panic(fmt.Sprintf("FuzzFrameKeyCode: unsupported key code %v", code))
	}
	return [FuzzFrameSize]byte{0, rune8, 0}
}

// FuzzFrameKeyCtrl encodes a printable rune with Ctrl held.
func FuzzFrameKeyCtrl(r rune) [FuzzFrameSize]byte {
	frame := FuzzFrameKey(r)
	frame[2] = 1 // mod8 % 4 == 1 → ModCtrl
	return frame
}

// FuzzFrameResize encodes a WindowSizeMsg frame with the (wIdx,
// hIdx) pair feeding the encoding's `idx*4` width/height map.
func FuzzFrameResize(wIdx, hIdx byte) [FuzzFrameSize]byte {
	return [FuzzFrameSize]byte{fuzzKindResize, wIdx, hIdx}
}

// LoadFuzzStyles returns the cached default skin. Thin wrapper kept
// for fuzz-call-site clarity; the cache lives in styles.go.
func LoadFuzzStyles(t *testing.T) *theme.Styles {
	t.Helper()
	return LoadStyles(t)
}
