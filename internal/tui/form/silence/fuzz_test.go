// SPDX-License-Identifier: Apache-2.0

package silence_test

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// fuzzNow / fuzzMatchers are hoisted so the per-iteration form
// constructor doesn't reallocate the matcher slice on every fuzz
// exec. Read-only from the fuzz fn; safe to share.
var (
	fuzzNow      = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	fuzzMatchers = []backend.Matcher{
		{Name: "alertname", Value: "HighCPU"},
		{Name: "severity", Value: "critical"},
	}
)

// FuzzSilenceForm is the depth fuzz target for the silence form
// — densest state machine in the repo (six fields with validation
// and duration parsing). Encoding matches FuzzApp's via the
// shared testutil codec. Oracle is panic-only.
func FuzzSilenceForm(f *testing.F) {
	addFormSeeds(f)

	f.Fuzz(func(t *testing.T, in []byte) {
		const maxFrames = 64
		msgs := testutil.DecodeFuzzMsgs(in)
		if len(msgs) > maxFrames {
			msgs = msgs[:maxFrames]
		}
		form := newFuzzForm(t)
		for _, msg := range msgs {
			next, _ := form.Update(msg)
			cast, ok := next.(*silenceform.Form)
			if !ok {
				// AutoPop on cancel/submit returns the same Form
				// type via the app.Page interface today. A future
				// change that swaps the concrete type would
				// silently mask coverage if we just `return`d, so
				// surface it loudly instead.
				t.Fatalf("Update returned unexpected concrete type %T; refresh the harness", next)
			}
			form = cast
			_ = form.View(80, 24)
		}
	})
}

func newFuzzForm(t *testing.T) *silenceform.Form {
	t.Helper()
	return silenceform.New(silenceform.Options{
		Clients:  map[string]silenceform.Client{"fuzz": &testutil.FakeSilenceClient{}},
		Tenant:   "fuzz",
		Styles:   testutil.LoadFuzzStyles(t),
		Now:      func() time.Time { return fuzzNow },
		Matchers: fuzzMatchers,
		Creator:  "fuzz",
	})
}

// addFormSeeds drives the form into distinct field-focus and
// content states so mutated inputs explore deep paths quickly.
func addFormSeeds(f *testing.F) {
	f.Helper()

	// Empty input — exercises the constructor path only.
	f.Add([]byte{})

	// Tab through every focusable field so each focus slot gets at
	// least one keypress. The fuzz fixture has a single tenant so
	// fieldTenant is disabled — Tab walks the five remaining fields.
	tabs := make([][testutil.FuzzFrameSize]byte, 0, 6)
	for range 6 {
		tabs = append(tabs, testutil.FuzzFrameKeyCode(tea.KeyTab))
	}
	f.Add(testutil.FuzzSeed(tabs...))

	// Realistic matcher append (matchers field is prefilled).
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('j'), testutil.FuzzFrameKey('o'), testutil.FuzzFrameKey('b'),
		testutil.FuzzFrameKey('='),
		testutil.FuzzFrameKey('a'), testutil.FuzzFrameKey('p'), testutil.FuzzFrameKey('i'),
	))

	// Realistic durations on the ends field.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKeyCode(tea.KeyTab), testutil.FuzzFrameKeyCode(tea.KeyTab),
		testutil.FuzzFrameKey('1'), testutil.FuzzFrameKey('h'),
	))
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKeyCode(tea.KeyTab), testutil.FuzzFrameKeyCode(tea.KeyTab),
		testutil.FuzzFrameKey('2'), testutil.FuzzFrameKey('h'),
		testutil.FuzzFrameKey('3'), testutil.FuzzFrameKey('0'), testutil.FuzzFrameKey('m'),
	))

	// Comment field with a few non-trivial bytes (control runes
	// must come from FuzzFrameKeyCode; printable seeding only
	// covers 0x20..0x7E).
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKeyCode(tea.KeyTab), testutil.FuzzFrameKeyCode(tea.KeyTab),
		testutil.FuzzFrameKeyCode(tea.KeyTab), testutil.FuzzFrameKeyCode(tea.KeyTab),
		testutil.FuzzFrameKey('!'), testutil.FuzzFrameKey('@'), testutil.FuzzFrameKey('#'),
	))

	// Submit chord.
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKeyCtrl('s')))

	// Cancel chord.
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKeyCode(tea.KeyEscape)))

	// Very long input (32 chars) to stress wrap math.
	long := make([][testutil.FuzzFrameSize]byte, 0, 32)
	for i := range 32 {
		long = append(long, testutil.FuzzFrameKey(rune('a'+(i%26))))
	}
	f.Add(testutil.FuzzSeed(long...))

	// Resize extremes.
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameResize(0, 0)))
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameResize(20, 6)))
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameResize(63, 63)))
}
