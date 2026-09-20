// SPDX-License-Identifier: Apache-2.0

package report

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
)

// The merge order is the answer the page exists for: "which file won"
// is unanswerable from the config alone once a drop-in overrides a
// base key. The renderer must print the list in load order, never
// sorted.
func TestConfig_ListsSourcesInLoadOrder(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, Config(&buf, ConfigInput{
		Sources: []config.Source{
			{Kind: config.SourceBase, Path: "/home/test/.config/a10r/a10r.yaml"},
			{Kind: config.SourceDropIn, Path: "/home/test/.config/a10r/config.d/10-prod.yaml"},
			{Kind: config.SourceDropIn, Path: "/home/test/.config/a10r/config.d/20-staging.yaml"},
			{Kind: config.SourceAliases, Path: "/home/test/.config/a10r/aliases.yaml"},
			{Kind: config.SourceKeys, Path: "/home/test/.config/a10r/keys/default.yaml"},
			{Kind: config.SourceSkin, Path: "/home/test/.config/a10r/skins/nord.yaml"},
		},
		Warnings: []string{
			"user skin shadows bundled skin of the same name (name=nord)",
		},
	}))

	assertGolden(t, "config_full.golden", buf.String())
}

// A first start has no config file and no warnings. Both sections
// must still print their header, because a page whose body is blank
// reads as a broken page rather than as an answer.
func TestConfig_EmptyStillPrintsBothSections(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, Config(&buf, ConfigInput{}))

	assertGolden(t, "config_empty.golden", buf.String())
}
