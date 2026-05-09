// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

// fakeClient is a small stand-in for backend.Client used across
// the per-check tests. Only the methods doctor exercises today
// (Status, ProbeReady) carry behaviour; the rest return zero
// values so the type satisfies the full backend.Client interface.
//
// Field names mirror what each method returns; setting them per
// test pins the relevant scenario without growing a builder.
type fakeClient struct {
	statusOut backend.Status
	statusErr error
	probeErr  error
}

func (f *fakeClient) Status(context.Context) (backend.Status, error) {
	return f.statusOut, f.statusErr
}

func (f *fakeClient) ProbeReady(context.Context) error { return f.probeErr }

// Stubs for the rest of backend.Client — these methods are not
// exercised by any check in this commit. Returning ErrUnsupported
// is consistent with vanilla.stubs.go's contract.
func (*fakeClient) ListAlerts(context.Context, backend.AlertFilter) ([]backend.Alert, error) {
	return nil, backend.ErrUnsupported
}

func (*fakeClient) ListAlertGroups(context.Context, backend.AlertFilter) ([]backend.AlertGroup, error) {
	return nil, backend.ErrUnsupported
}

func (*fakeClient) ListSilences(context.Context, backend.SilenceFilter) ([]backend.Silence, error) {
	return nil, backend.ErrUnsupported
}

func (*fakeClient) GetSilence(context.Context, string) (backend.Silence, error) {
	return backend.Silence{}, backend.ErrUnsupported
}

func (*fakeClient) ListReceivers(context.Context) ([]backend.Receiver, error) {
	return nil, backend.ErrUnsupported
}

func (*fakeClient) CreateSilence(context.Context, backend.SilenceSpec) (string, error) {
	return "", backend.ErrUnsupported
}

func (*fakeClient) UpdateSilence(context.Context, string, backend.SilenceSpec) error {
	return backend.ErrUnsupported
}

func (*fakeClient) ExpireSilence(context.Context, string) error {
	return backend.ErrUnsupported
}

func (*fakeClient) GetConfig(context.Context) (backend.MimirConfig, error) {
	return backend.MimirConfig{}, backend.ErrUnsupported
}

func (*fakeClient) SetConfig(context.Context, backend.MimirConfig) error {
	return backend.ErrUnsupported
}

func (*fakeClient) DeleteConfig(context.Context) error {
	return backend.ErrUnsupported
}

func (*fakeClient) ListTenantConfigs(context.Context) ([]backend.TenantConfig, error) {
	return nil, backend.ErrUnsupported
}

func (*fakeClient) RingStatus(context.Context) (backend.Ring, error) {
	return backend.Ring{}, backend.ErrUnsupported
}
func (*fakeClient) Capabilities() backend.Caps { return backend.Caps{} }

// Compile-time assertions — fakeClient must satisfy both the full
// Client interface and the smaller Prober interface so the test
// suite catches a future shape drift between the two.
var (
	_ backend.Client = (*fakeClient)(nil)
	_ backend.Prober = (*fakeClient)(nil)
)

func TestReachabilityChecker(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		client  backend.Client
		wantSev Severity
		wantMsg string // substring; "" skips
	}{
		{name: "nil client", client: nil, wantSev: SeverityError, wantMsg: "client construction failed"},
		{name: "probe ok", client: &fakeClient{}, wantSev: SeverityOK},
		{name: "probe error", client: &fakeClient{probeErr: errors.New("connection refused")}, wantSev: SeverityError, wantMsg: "connection refused"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ReachabilityChecker{}.Run(t.Context(), config.Backend{Name: "x"}, tc.client)
			require.Equal(t, tc.wantSev, got.Severity)
			if tc.wantMsg != "" {
				require.Contains(t, got.Message, tc.wantMsg)
			}
		})
	}
}

func TestAuthChecker(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		client  backend.Client
		wantSev Severity
		wantMsg string
	}{
		{name: "nil client", client: nil, wantSev: SeverityError},
		{name: "ok", client: &fakeClient{}, wantSev: SeverityOK},
		{name: "401 unauthorised", client: &fakeClient{statusErr: backend.ErrUnauthorized}, wantSev: SeverityError, wantMsg: "authentication rejected"},
		{name: "transport unreachable", client: &fakeClient{statusErr: backend.ErrUnreachable}, wantSev: SeverityWarning, wantMsg: "backend unreachable"},
		{name: "other error", client: &fakeClient{statusErr: errors.New("decode failure")}, wantSev: SeverityError, wantMsg: "decode failure"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := AuthChecker{}.Run(t.Context(), config.Backend{Name: "x"}, tc.client)
			require.Equal(t, tc.wantSev, got.Severity)
			if tc.wantMsg != "" {
				require.Contains(t, got.Message, tc.wantMsg)
			}
		})
	}
}

func TestVersionFloorChecker(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		client  backend.Client
		wantSev Severity
		wantMsg string
	}{
		{
			name:    "nil client",
			client:  nil,
			wantSev: SeverityError,
		},
		{
			name:    "status error → warning",
			client:  &fakeClient{statusErr: errors.New("nope")},
			wantSev: SeverityWarning,
			wantMsg: "status unavailable",
		},
		{
			name:    "unparseable version",
			client:  &fakeClient{statusOut: backend.Status{Version: backend.VersionInfo{Version: "not-a-version"}}},
			wantSev: SeverityWarning,
			wantMsg: "unrecognised version",
		},
		{
			name:    "below floor",
			client:  &fakeClient{statusOut: backend.Status{Version: backend.VersionInfo{Version: "0.27.0"}}},
			wantSev: SeverityError,
			wantMsg: "a10r requires >= 0.28.1",
		},
		{
			name:    "at floor",
			client:  &fakeClient{statusOut: backend.Status{Version: backend.VersionInfo{Version: "0.28.1"}}},
			wantSev: SeverityOK,
			wantMsg: "0.28.1 >= floor 0.28.1",
		},
		{
			name:    "above floor",
			client:  &fakeClient{statusOut: backend.Status{Version: backend.VersionInfo{Version: "1.2.3"}}},
			wantSev: SeverityOK,
			wantMsg: "1.2.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := VersionFloorChecker{}.Run(t.Context(), config.Backend{Name: "x"}, tc.client)
			require.Equal(t, tc.wantSev, got.Severity)
			if tc.wantMsg != "" {
				require.Contains(t, got.Message, tc.wantMsg)
			}
		})
	}
}

func TestDefaultCheckers_RegistrationOrder(t *testing.T) {
	t.Parallel()

	// Pinning the order doubles as a regression guard for
	// docs/end-users/*.md examples that reference the order.
	got := make([]string, 0, len(DefaultCheckers()))
	for _, c := range DefaultCheckers() {
		got = append(got, c.Name())
	}
	require.Equal(t, []string{"reachability", "auth", "version-floor"}, got)
}
