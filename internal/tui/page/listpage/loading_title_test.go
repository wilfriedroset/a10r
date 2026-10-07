// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestLoadingTitle(t *testing.T) {
	t.Parallel()

	sp := spinner.New(spinner.WithSpinner(spinner.Points))
	u := &listpage.PollingUI{Spinner: sp}

	for _, noun := range []string{"alerts", "silences", "receivers"} {
		t.Run(noun, func(t *testing.T) {
			t.Parallel()
			got := u.LoadingTitle(noun, lipgloss.NewStyle())
			suffix := " loading " + noun + "…"
			require.Truef(t, strings.HasSuffix(got, suffix), "want suffix %q, got %q", suffix, got)
			frame := strings.TrimSuffix(got, suffix)
			require.NotEmpty(t, frame, "spinner frame should precede the loading suffix")
		})
	}
}

func TestLoadingTitle_StyleAppliedPerCall(t *testing.T) {
	t.Parallel()

	// The spinner keeps its style as a plain value, so a page built
	// before the auto-theme swap must still pick up the new accent.
	u := &listpage.PollingUI{Spinner: spinner.New(spinner.WithSpinner(spinner.Points))}

	plain := u.LoadingTitle("alerts", lipgloss.NewStyle())
	painted := u.LoadingTitle("alerts", lipgloss.NewStyle().Foreground(lipgloss.Color("#ff00ff")))
	require.NotEqual(t, plain, painted, "a new style must reach the rendered frame")
}
