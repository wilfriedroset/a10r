// SPDX-License-Identifier: Apache-2.0

package uistate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore_ForgetAfterExternalDeleteKeepsWriting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	flushed := make(chan struct{}, 1)
	s := openWithFlushHook(dir, func() { flushed <- struct{}{} })

	s.SetScope("prod")
	<-flushed
	require.NoError(t, os.Remove(path))

	s.SetScope("all")
	<-flushed

	s.SetScope("staging")
	require.NoError(t, s.Close())

	b, err := os.ReadFile(path)
	require.NoError(t, err, "a remove of an absent file must not disable the store")
	require.Equal(t, "scope: staging\n", string(b))
}
