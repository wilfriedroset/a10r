// SPDX-License-Identifier: Apache-2.0

package backend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStateConstants_ArePinned(t *testing.T) {
	t.Parallel()

	// Wire-level strings are part of the contract with /api/v2/alerts
	// and /api/v2/silences. Renaming a constant must trip this test
	// so a release notes entry is forced.
	require.Equal(t, AlertStateActive, AlertState("active"))
	require.Equal(t, AlertStateSuppressed, AlertState("suppressed"))
	require.Equal(t, AlertStateUnprocessed, AlertState("unprocessed"))

	require.Equal(t, SilenceStateActive, SilenceState("active"))
	require.Equal(t, SilenceStatePending, SilenceState("pending"))
	require.Equal(t, SilenceStateExpired, SilenceState("expired"))
}

func TestClient_InterfaceShape(t *testing.T) {
	t.Parallel()

	// Compile-time assertion that the interface contract is what
	// implementations will need to satisfy. The fakeClient below
	// covers every method; if the interface grows or shrinks, the
	// build breaks here — exactly where it should.
	var _ Client = (*fakeClient)(nil)
}

// fakeClient satisfies Client with no-op methods. Used purely to
// pin the interface shape at compile time; the concrete
// implementations live in subpackages (vanilla, mimir, multi).
type fakeClient struct {
	caps Caps
}

func (*fakeClient) ListAlerts(context.Context, AlertFilter) ([]Alert, error) {
	return nil, nil
}

func (*fakeClient) ListSilences(context.Context, SilenceFilter) ([]Silence, error) {
	return nil, nil
}

func (*fakeClient) GetSilence(context.Context, string) (Silence, error) {
	return Silence{}, nil
}

func (*fakeClient) ListReceivers(context.Context) ([]Receiver, error) {
	return nil, nil
}

func (*fakeClient) Status(context.Context) (Status, error) {
	return Status{Uptime: time.Hour}, nil
}

func (*fakeClient) CreateSilence(context.Context, SilenceSpec) (string, error) {
	return "", nil
}

func (*fakeClient) UpdateSilence(context.Context, string, SilenceSpec) error {
	return nil
}

func (*fakeClient) ExpireSilence(context.Context, string) error { return nil }

func (*fakeClient) GetConfig(context.Context) (MimirConfig, error) {
	return MimirConfig{}, ErrUnsupported
}

func (*fakeClient) SetConfig(context.Context, MimirConfig) error {
	return ErrUnsupported
}

func (*fakeClient) DeleteConfig(context.Context) error { return ErrUnsupported }

func (*fakeClient) ListTenantConfigs(context.Context) ([]TenantConfig, error) {
	return nil, ErrUnsupported
}

func (*fakeClient) RingStatus(context.Context) (Ring, error) {
	return Ring{}, ErrUnsupported
}

func (f *fakeClient) Capabilities() Caps { return f.caps }
