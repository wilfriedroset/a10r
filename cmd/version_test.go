// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrintVersion_ExactFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, printVersion(&buf))

	// Pin to the exact line a `version` consumer pipes through `awk`,
	// so a regression to e.g. `a10r<version>commit=…` (missing space)
	// is caught — Contains() would let that pass.
	want := "a10r " + version + " commit=" + commit + " built=" + date + "\n"
	require.Equal(t, want, buf.String())
}
