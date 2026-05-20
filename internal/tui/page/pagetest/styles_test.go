// SPDX-License-Identifier: Apache-2.0

package pagetest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
)

func TestStyles_LoadsDefaultSkin(t *testing.T) {
	t.Parallel()

	// Styles must return a populated *theme.Styles — a zero-value
	// regression here would silently let every downstream page test
	// render blank rows while still passing string-Contains checks
	// on chrome text.
	s := pagetest.Styles(t)
	require.NotNil(t, s, "Styles must never return nil")
	require.NotNil(t, s.Severity.Critical.GetForeground(),
		"default skin must resolve the critical-severity foreground")
}

func TestStyles_ReturnsCachedPointer(t *testing.T) {
	t.Parallel()

	// The sync.Once contract: every call inside one test binary
	// returns the same *theme.Styles. Tests must treat it as
	// read-only; the assertion here is structural (identity) so a
	// regression that re-parses on every call surfaces immediately.
	a := pagetest.Styles(t)
	b := pagetest.Styles(t)
	require.Same(t, a, b,
		"Styles must cache the loaded *theme.Styles so the YAML parse runs once per test binary")
}
