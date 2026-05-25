// SPDX-License-Identifier: Apache-2.0

package backendtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/backendtest"
)

// Exercises every method on ClientStub so a future return-value drift
// (e.g. an accidental nil-error stub on a new capability) surfaces
// loud. The compile-time assertion in clientstub.go already pins the
// interface shape; this test pins the behaviour contract.
func TestClientStub_AllMethodsReturnErrUnsupported(t *testing.T) {
	t.Parallel()
	var c backend.Client = backendtest.ClientStub{}
	ctx := t.Context()

	_, err := c.ListAlerts(ctx, backend.AlertFilter{})
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.ListAlertGroups(ctx, backend.AlertFilter{})
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.ListSilences(ctx, backend.SilenceFilter{})
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.GetSilence(ctx, "id")
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.ListReceivers(ctx)
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.Status(ctx)
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.CreateSilence(ctx, backend.SilenceSpec{})
	require.ErrorIs(t, err, backend.ErrUnsupported)

	require.ErrorIs(t, c.UpdateSilence(ctx, "id", backend.SilenceSpec{}), backend.ErrUnsupported)
	require.ErrorIs(t, c.ExpireSilence(ctx, "id"), backend.ErrUnsupported)

	_, err = c.GetConfig(ctx)
	require.ErrorIs(t, err, backend.ErrUnsupported)

	require.ErrorIs(t, c.SetConfig(ctx, backend.MimirConfig{}), backend.ErrUnsupported)
	require.ErrorIs(t, c.DeleteConfig(ctx), backend.ErrUnsupported)

	_, err = c.ListTenantConfigs(ctx)
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.RingStatus(ctx)
	require.ErrorIs(t, err, backend.ErrUnsupported)

	require.Equal(t, backend.Caps{}, c.Capabilities())
}
