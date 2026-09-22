// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// TestApp_FilterTagFollowsThePageClassification is the end-to-end
// witness that the title tag and the rows come from one reading of
// the buffer. The chrome no longer classifies for a page that owns
// its buffer, so only a real page behind a real prompt proves the
// label the user sees is the one the page filtered with. The unit
// tests on either side of the seam either fake the page or never
// see a prompt.
func TestApp_FilterTagFollowsThePageClassification(t *testing.T) {
	t.Parallel()

	m := bootApp(t, nil)
	m = step(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	for _, r := range "~highcpu" {
		m = step(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	out := testutil.StripStyle(m.(*app.App).View().Content)
	require.Contains(t, out, "</~highcpu>")
	require.Contains(t, out, "[fuzzy]",
		"the tag is the page's own classification of the buffer the prompt carries")
}
