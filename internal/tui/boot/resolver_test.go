// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
)

// TestNewResolver_GroupsCatalogue pins the canonical+synonym shape
// the help overlay (commit 7) reads from the resolver. Built-in
// singletons (alerts, status) get one-name groups; synonym pairs
// (q/quit, silences/sil, receivers/rec, groups/gr, tenant/tenants)
// fold onto a single row each. A future contributor dropping a
// synonym or renaming a canonical fails this test loudly rather
// than silently regressing the COMMANDS column.
func TestNewResolver_GroupsCatalogue(t *testing.T) {
	t.Parallel()

	// Handlers close over *pageEnv but the catalogue assertion only
	// reads the alias names — a zero-value env is sufficient because
	// no handler runs during the test.
	r := newResolver(&pageEnv{})

	require.Equal(t,
		[]cmdbar.AliasGroup{
			{Names: []string{"alerts"}},
			{Names: []string{"groups", "gr"}},
			{Names: []string{"q", "quit"}},
			{Names: []string{"receivers", "rec"}},
			{Names: []string{"silences", "sil"}},
			{Names: []string{"status"}},
			{Names: []string{"tenant", "tenants"}},
		},
		r.Groups())
}

// TestNewResolver_QuitAliases pins that every alias in the quit
// group (the vim-canonical `:q` and the spelled-out `:quit`) emits
// the QuitRequestedMsg precursor, so the page-stack Close cascade
// runs before bubbletea stops. Aliases are read from the catalogue
// so adding a synonym there is covered without restating it here.
func TestNewResolver_QuitAliases(t *testing.T) {
	t.Parallel()

	r := newResolver(&pageEnv{})

	var quit []string
	for _, g := range r.Groups() {
		if g.Names[0] == "q" {
			quit = g.Names
		}
	}
	require.NotEmpty(t, quit, "resolver must register a quit alias group")

	for _, alias := range quit {
		t.Run(alias, func(t *testing.T) {
			t.Parallel()
			cmd, err := r.Resolve(alias)
			require.NoError(t, err)
			require.NotNil(t, cmd)
			require.IsType(t, app.QuitRequestedMsg{}, cmd())
		})
	}
}
