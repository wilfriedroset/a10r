//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package xdg

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAtomicWrite_DirSyncOutcomes pins the two answers a directory
// flush can get. The write is already at its destination in both
// cases, so the only question is whether the caller hears about it.
func TestAtomicWrite_DirSyncOutcomes(t *testing.T) {
	t.Parallel()

	refused := errors.New("no fsync here")
	cases := []struct {
		name    string
		dirErr  error
		wantErr error
	}{
		{name: "a refused flush fails the write", dirErr: refused, wantErr: refused},
		{name: "a filesystem without the call does not", dirErr: errors.ErrUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "state.yaml")
			err := atomicWriteWith(path, []byte("data\n"), func(f *os.File) error {
				st, statErr := f.Stat()
				require.NoError(t, statErr)
				if st.IsDir() {
					return tc.dirErr
				}
				return nil
			})

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			got, readErr := os.ReadFile(path)
			require.NoError(t, readErr, "the rename already happened, whatever the flush said")
			require.Equal(t, "data\n", string(got))
		})
	}
}
