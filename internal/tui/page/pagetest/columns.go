// SPDX-License-Identifier: Apache-2.0

package pagetest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
)

// RequireColumnRulesMatchPage fails when the config column validation
// of a page drifts from the page. config cannot import the pages, so
// it keeps its own copy of their built-in titles and bound letters.
// Both pages word the rejections alike, so the test compares on a
// fragment. A key a page handles but leaves out of Bindings() escapes
// this check as well as the hint strip.
func RequireColumnRulesMatchPage(t *testing.T, wrap func(...config.Column) config.Config,
	titles []string, bindings []action.Action,
) {
	t.Helper()

	for _, title := range titles {
		cfg := wrap(config.Column{Label: "drift_probe", Title: title})
		require.ErrorContains(t, cfg.Validate(), "is a built-in column title",
			"config does not know the built-in title %q", title)
	}

	bound := boundLetters(bindings)
	for c := 'A'; c <= 'Z'; c++ {
		key := string(c)
		cfg := wrap(config.Column{Label: "drift_probe", SortKey: key})
		if strings.Contains(bound, key) {
			require.ErrorContains(t, cfg.Validate(), "is already bound",
				"config accepts sort_key %q, which the page binds", key)
			continue
		}
		require.NoError(t, cfg.Validate(), "config rejects sort_key %q, which the page leaves free", key)
	}
}

// boundLetters adds G because the cursor binds it on every table, and
// the page Bindings() never lists it.
func boundLetters(bindings []action.Action) string {
	out := "G"
	for _, b := range bindings {
		key := strings.TrimPrefix(b.Key, "Shift+")
		if len(key) == 1 && key[0] >= 'A' && key[0] <= 'Z' && !strings.Contains(out, key) {
			out += key
		}
	}
	return out
}
