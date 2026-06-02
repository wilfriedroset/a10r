// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"
)

func TestDecodeFuzzMsgs_FrameKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []byte
		want []tea.Msg
	}{
		{
			name: "empty",
			in:   []byte{},
			want: []tea.Msg{},
		},
		{
			name: "tail under one frame is discarded",
			in:   []byte{0, 0},
			want: []tea.Msg{},
		},
		{
			name: "noop frame produces nothing",
			in:   []byte{15, 0, 0},
			want: []tea.Msg{},
		},
		{
			name: "resize frame multiplies idx by 4",
			in:   []byte{14, 5, 7},
			want: []tea.Msg{tea.WindowSizeMsg{Width: 20, Height: 28}},
		},
		{
			name: "resize 0,0 reaches zero extreme",
			in:   []byte{14, 0, 0},
			want: []tea.Msg{tea.WindowSizeMsg{Width: 0, Height: 0}},
		},
		{
			name: "two frames decode independently",
			in:   []byte{14, 1, 1, 15, 0, 0},
			want: []tea.Msg{tea.WindowSizeMsg{Width: 4, Height: 4}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DecodeFuzzMsgs(tc.in)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDecodeFuzzMsgs_KeyFrameSpecialAndPrintable(t *testing.T) {
	t.Parallel()

	specials := map[byte]rune{
		fuzzKeyEnter:     tea.KeyEnter,
		fuzzKeyEscape:    tea.KeyEscape,
		fuzzKeyTab:       tea.KeyTab,
		fuzzKeyBackspace: tea.KeyBackspace,
		fuzzKeySpace:     tea.KeySpace,
	}
	for rune8, want := range specials {
		got := DecodeFuzzMsgs([]byte{0, rune8, 0})
		require.Len(t, got, 1)
		k := got[0].(tea.KeyPressMsg)
		require.Equal(t, want, k.Code)
	}

	// Printable branch: rune8 = 5 maps to 0x20 + 0 = ' ' (space
	// glyph; tea.KeySpace special path is reserved for the named
	// slot above so byte 5 routes via printable arithmetic).
	got := DecodeFuzzMsgs([]byte{0, 5, 0})
	require.Len(t, got, 1)
	k := got[0].(tea.KeyPressMsg)
	require.Equal(t, ' ', k.Code)
	require.Equal(t, " ", k.Text)
}

func TestDecodeFuzzMsgs_KeyFrameModifiers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mod  byte
		want tea.KeyMod
	}{
		{0, 0},
		{1, tea.ModCtrl},
		{2, tea.ModShift},
		{3, tea.ModAlt},
		{5, tea.ModCtrl}, // wraps via mod8%4
	}
	for _, tc := range cases {
		got := DecodeFuzzMsgs([]byte{0, 5, tc.mod})
		require.Len(t, got, 1)
		k := got[0].(tea.KeyPressMsg)
		require.Equal(t, tc.want, k.Mod)
	}
}

func TestFuzzFrameKey_RoundTripsToPrintableBranch(t *testing.T) {
	t.Parallel()

	for r := rune(fuzzPrintableLo); r < fuzzPrintableLo+fuzzPrintableLen; r++ {
		frame := FuzzFrameKey(r)
		got := DecodeFuzzMsgs(frame[:])
		require.Len(t, got, 1, "frame for %q decoded to %d msgs", r, len(got))
		k := got[0].(tea.KeyPressMsg)
		require.Equal(t, r, k.Code, "round-trip mismatch for %q", r)
	}
}

func TestFuzzFrameKeyCode_AllSupported(t *testing.T) {
	t.Parallel()
	for _, code := range []rune{tea.KeyEnter, tea.KeyEscape, tea.KeyTab, tea.KeyBackspace, tea.KeySpace} {
		frame := FuzzFrameKeyCode(code)
		got := DecodeFuzzMsgs(frame[:])
		require.Len(t, got, 1)
		k := got[0].(tea.KeyPressMsg)
		require.Equal(t, code, k.Code)
	}
}

func TestFuzzFrameKeyCode_PanicsOnUnsupported(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		FuzzFrameKeyCode('z') // 'z' is a printable rune, not one of the named codes
	})
}

func TestFuzzFrameKeyCtrl_SetsModCtrl(t *testing.T) {
	t.Parallel()
	frame := FuzzFrameKeyCtrl('t')
	got := DecodeFuzzMsgs(frame[:])
	require.Len(t, got, 1)
	k := got[0].(tea.KeyPressMsg)
	require.Equal(t, 't', k.Code)
	require.Equal(t, tea.ModCtrl, k.Mod)
}

func TestFuzzFrameResize_RoundTrip(t *testing.T) {
	t.Parallel()
	frame := FuzzFrameResize(10, 5)
	got := DecodeFuzzMsgs(frame[:])
	require.Equal(t, []tea.Msg{tea.WindowSizeMsg{Width: 40, Height: 20}}, got)
}

func TestFuzzSeed_ConcatenatesFrames(t *testing.T) {
	t.Parallel()
	out := FuzzSeed(FuzzFrameKey('a'), FuzzFrameKeyCode(tea.KeyEnter), FuzzFrameResize(0, 0))
	require.Len(t, out, 3*FuzzFrameSize)

	msgs := DecodeFuzzMsgs(out)
	require.Len(t, msgs, 3)
	require.Equal(t, 'a', msgs[0].(tea.KeyPressMsg).Code)
	require.Equal(t, tea.KeyEnter, msgs[1].(tea.KeyPressMsg).Code)
	require.Equal(t, tea.WindowSizeMsg{Width: 0, Height: 0}, msgs[2])
}
