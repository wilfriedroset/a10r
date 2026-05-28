// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
)

// TestNewResolver_GroupsCatalogue pins the canonical+synonym shape
// the help overlay (commit 7) reads from the resolver. Built-in
// singletons (alerts, status, q) get one-name groups; synonym
// pairs (silences/sil, receivers/rec, groups/gr, tenant/tenants)
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
			{Names: []string{"q"}},
			{Names: []string{"receivers", "rec"}},
			{Names: []string{"silences", "sil"}},
			{Names: []string{"status"}},
			{Names: []string{"tenant", "tenants"}},
		},
		r.Groups())
}
