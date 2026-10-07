// SPDX-License-Identifier: Apache-2.0

package xdg

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAtomicWrite_SyncFailureIsFatal pins the call and the error
// path. The durability the sync buys is not testable without a crash
// harness, so the test proves only that a refused flush stops the
// write instead of reporting success.
func TestAtomicWrite_SyncFailureIsFatal(t *testing.T) {
	t.Parallel()

	errSync := errors.New("disk said no")
	path := filepath.Join(t.TempDir(), "state.yaml")
	err := atomicWriteWith(path, []byte("data\n"), func(*os.File) error { return errSync })

	require.ErrorIs(t, err, errSync)
	require.NoFileExists(t, path, "a write that could not flush must not appear at the destination")
	entries, readErr := os.ReadDir(filepath.Dir(path))
	require.NoError(t, readErr)
	require.Empty(t, entries, "the temp file must go with the failed write")
}
