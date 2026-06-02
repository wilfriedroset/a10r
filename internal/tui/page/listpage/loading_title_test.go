// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestLoadingTitle(t *testing.T) {
	t.Parallel()

	sp := spinner.New(spinner.WithSpinner(spinner.Points))
	u := &listpage.PollingUI{Spinner: sp}

	for _, noun := range []string{"alerts", "silences", "groups"} {
		t.Run(noun, func(t *testing.T) {
			t.Parallel()
			got := u.LoadingTitle(noun)
			suffix := " loading " + noun + "…"
			require.Truef(t, strings.HasSuffix(got, suffix), "want suffix %q, got %q", suffix, got)
			frame := strings.TrimSuffix(got, suffix)
			require.NotEmpty(t, frame, "spinner frame should precede the loading suffix")
		})
	}
}
