// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestClearMarks_DropsTheMarks(t *testing.T) {
	t.Parallel()

	var b listpage.Base
	marks := map[string]struct{}{"fp-1": {}, "fp-2": {}}

	cmd := listpage.ClearMarks(&b, marks)

	require.Empty(t, marks)
	require.NotNil(t, cmd)
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok)
	require.Equal(t, "marks cleared", msg.Text)
}

func TestClearMarks_StaysQuietWithNoMarks(t *testing.T) {
	t.Parallel()

	var b listpage.Base
	b.Visual.Start("fp-1")

	require.Nil(t, listpage.ClearMarks(&b, map[string]struct{}{}))
	require.False(t, b.Visual.On(), "the open range goes even when there is nothing marked")
}

func TestClearMarks_DropsAnOpenRangeWithTheMarks(t *testing.T) {
	t.Parallel()

	var b listpage.Base
	b.Visual.Start("fp-1")

	require.NotNil(t, listpage.ClearMarks(&b, map[string]struct{}{"fp-1": {}}))
	require.False(t, b.Visual.On())
}
