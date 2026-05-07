// SPDX-License-Identifier: Apache-2.0

package testutil_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func TestLoadStyles_LoadsDefaultSkin(t *testing.T) {
	t.Parallel()

	// Guards against the loader regressing to a zero-value Styles
	// — that drift would silently let every downstream page test
	// keep passing while rendering blank rows.
	styles := testutil.LoadStyles(t)
	require.NotNil(t, styles.Severity.Critical.GetForeground(),
		"default skin must resolve the critical-severity foreground")
}
