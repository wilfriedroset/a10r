// SPDX-License-Identifier: Apache-2.0

package listcmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/listcmd"
)

func TestJSONRenderer_EmptyIsArrayNotNull(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, listcmd.JSONRenderer[int](&buf, nil))
	require.Equal(t, "[]", strings.TrimSpace(buf.String()),
		"an empty result must encode as [] so `| jq '.[]'` is a clean no-op, not null")
}
