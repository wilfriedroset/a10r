// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteHints(t *testing.T) {
	t.Parallel()

	ok := func(id string) writeResult { return writeResult{Tenant: "t", ID: id, Status: writeStatusCreated} }
	fail := writeResult{Tenant: "t", Status: writeStatusError, Error: "boom"}

	t.Run("createdHint undoes via variadic expire", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, createdHint(nil))
		require.Empty(t, createdHint([]writeResult{fail}))
		require.Equal(t, "expire with: a10r silences expire a", createdHint([]writeResult{ok("a")}))
		require.Equal(t, "expire with: a10r silences expire a b", createdHint([]writeResult{ok("a"), ok("b")}))
		require.Equal(t, "expire with: a10r silences expire a", createdHint([]writeResult{ok("a"), fail}))
	})

	t.Run("expiredHint undoes via recreate only when single", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, expiredHint(nil))
		require.Equal(t, "recreate with: a10r silences recreate a", expiredHint([]writeResult{ok("a")}))
		require.Empty(t, expiredHint([]writeResult{ok("a"), ok("b")}), "multiple distinct ids suppress")
		require.Equal(t, "recreate with: a10r silences recreate a",
			expiredHint([]writeResult{ok("a"), ok("a")}), "one id mirrored across tenants still collapses")
	})

	t.Run("updatedHint verifies via get", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, updatedHint(nil))
		require.Equal(t, "verify with: a10r silences get a", updatedHint([]writeResult{ok("a")}))
		require.Equal(t, "verify with: a10r silences get a", updatedHint([]writeResult{ok("a"), ok("a")}))
	})
}
