// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
)

// The wide column adds Shift+W but stays out of columns().
func TestColumnRules_ConfigMatchesThePage(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: "pod", Wide: true}})
	titles := make([]string, 0, 4)
	for _, c := range p.columns() {
		titles = append(titles, c.Title)
	}

	require.Equal(t, []string{"SEVERITY", "INSTANCE", "STATE", "AGE"}, titles)
	pagetest.RequireColumnRulesMatchPage(t, withGroupColumns, titles, p.Bindings())
}
