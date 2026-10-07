// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	tea "charm.land/bubbletea/v2"
)

// FakeClipboard records every Copy call so a test can assert what
// reached the clipboard without a terminal. Satisfies the pages'
// clipboard seam; the nil Cmd it returns is what a copy contributes
// to the batch anyway.
type FakeClipboard struct {
	Last  string
	Calls int
}

// Copy records s and reports no work for the bubbletea loop.
func (f *FakeClipboard) Copy(s string) tea.Cmd {
	f.Calls++
	f.Last = s
	return nil
}
