// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	a10rtls "github.com/wilfriedroset/a10r/internal/backend/tls"
	"github.com/wilfriedroset/a10r/internal/config"
)

// fakeClient is a small stand-in for backend.Client used across
// the per-check tests. Only the methods doctor exercises today
// (Status, ProbeReady, ProbeReadyAt) carry behaviour; the rest
// return zero values so the type satisfies the full backend.Client
// interface.
//
// Field names mirror what each method returns; setting them per
// test pins the relevant scenario without growing a builder.
type fakeClient struct {
	statusOut   backend.Status
	statusErr   error
	probeErr    error
	probeAtTime time.Time
	probeAtErr  error
}

func (f *fakeClient) Status(context.Context) (backend.Status, error) {
	return f.statusOut, f.statusErr
}

func (f *fakeClient) ProbeReady(context.Context) error { return f.probeErr }

func (f *fakeClient) ProbeReadyAt(context.Context) (time.Time, error) {
	return f.probeAtTime, f.probeAtErr
}

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
	require.Equal(t, []string{
		"reachability", "auth", "version-floor",
		"tls-expiry", "capabilities", "clock-skew",
	}, got)
}

// fixedNow returns a clock function that always reports t. Used
// across the TLS-expiry and clock-skew tests so the boundary cases
// (cert in 30 days, server 30s ahead) pin deterministically.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

// retryableError stands in for the vanilla client's transientError
// wrapper used for 5xx / 429 responses. Implements
// backend.Retryabler so backend.Retryable(err) returns true,
// driving the CapabilitiesChecker's transport-failure branch.
type retryableError struct{}

func (retryableError) Error() string   { return "transient HTTP 503" }
func (retryableError) Retryable() bool { return true }

func TestTLSExpiryChecker(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	makeProbe := func(notAfter time.Time, err error) tlsCertProbe {
		return func(context.Context, string) (*x509.Certificate, error) {
			if err != nil {
				return nil, err
			}
			return &x509.Certificate{NotAfter: notAfter}, nil
		}
	}

	cases := []struct {
		name    string
		backend config.Backend
		probe   tlsCertProbe
		wantSev Severity
		wantMsg string
	}{
		{
			name:    "60 days valid → ok",
			backend: config.Backend{Name: "x", URL: "https://am.internal"},
			probe:   makeProbe(now.Add(60*24*time.Hour), nil),
			wantSev: SeverityOK,
			wantMsg: "valid until",
		},
		{
			name:    "15 days valid → warning",
			backend: config.Backend{Name: "x", URL: "https://am.internal"},
			probe:   makeProbe(now.Add(15*24*time.Hour), nil),
			wantSev: SeverityWarning,
			wantMsg: "expires in",
		},
		{
			name:    "exactly at threshold (30 days) → ok",
			backend: config.Backend{Name: "x", URL: "https://am.internal"},
			probe:   makeProbe(now.Add(30*24*time.Hour), nil),
			wantSev: SeverityOK,
			wantMsg: "valid until",
		},
		{
			name:    "expired → error",
			backend: config.Backend{Name: "x", URL: "https://am.internal"},
			probe:   makeProbe(now.Add(-24*time.Hour), nil),
			wantSev: SeverityError,
			wantMsg: "expired",
		},
		{
			name:    "http url → ok with n/a",
			backend: config.Backend{Name: "x", URL: "http://am.internal"},
			probe:   makeProbe(time.Time{}, a10rtls.ErrNotHTTPS),
			wantSev: SeverityOK,
			wantMsg: "n/a",
		},
		{
			name:    "self-signed / unknown authority → error",
			backend: config.Backend{Name: "x", URL: "https://am.internal"},
			probe:   makeProbe(time.Time{}, errors.New("x509: certificate signed by unknown authority")),
			wantSev: SeverityError,
			wantMsg: "tls probe failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch := TLSExpiryChecker{probe: tc.probe, now: fixedNow(now)}
			got := ch.Run(t.Context(), tc.backend, &fakeClient{})
			require.Equal(t, tc.wantSev, got.Severity)
			require.Contains(t, got.Message, tc.wantMsg)
		})
	}
}

func TestCapabilitiesChecker(t *testing.T) {
	t.Parallel()

	// Each test pins a per-cap probes map so the checker exercises
	// only the flags the test sets — no implicit dependency on the
	// production capabilityProbes map.
	okProbe := func(context.Context, backend.Client) error { return nil }
	notFoundProbe := func(context.Context, backend.Client) error {
		return errors.New("HTTP 404")
	}
	unsupportedProbe := func(context.Context, backend.Client) error {
		return backend.ErrUnsupported
	}
	transportProbe := func(context.Context, backend.Client) error {
		return backend.ErrUnreachable
	}
	retryableProbe := func(context.Context, backend.Client) error {
		// stand-in for a 5xx / 429 response: the vanilla client wraps
		// these as a *transientError satisfying Retryable() = true.
		return retryableError{}
	}

	cases := []struct {
		name    string
		caps    config.Capabilities
		probes  map[string]capabilityProbe
		client  backend.Client
		wantSev Severity
		wantMsg string
	}{
		{
			name:    "nil client → error",
			client:  nil,
			wantSev: SeverityError,
			wantMsg: "client construction failed",
		},
		{
			name:    "no caps configured → ok",
			client:  &fakeClient{},
			caps:    config.Capabilities{},
			wantSev: SeverityOK,
			wantMsg: "no capabilities configured",
		},
		{
			name:    "all enabled caps respond → ok",
			client:  &fakeClient{},
			caps:    config.Capabilities{ConfigAPI: true, TenantAdmin: true, Ring: true},
			probes:  map[string]capabilityProbe{"config_api": okProbe, "tenant_admin": okProbe, "ring": okProbe},
			wantSev: SeverityOK,
			wantMsg: "verified",
		},
		{
			name:    "ErrUnsupported mismatch → error",
			client:  &fakeClient{},
			caps:    config.Capabilities{ConfigAPI: true},
			probes:  map[string]capabilityProbe{"config_api": unsupportedProbe},
			wantSev: SeverityError,
			wantMsg: "capability mismatch",
		},
		{
			name:    "transport error → warning, not mismatch",
			client:  &fakeClient{},
			caps:    config.Capabilities{ConfigAPI: true},
			probes:  map[string]capabilityProbe{"config_api": transportProbe},
			wantSev: SeverityWarning,
			wantMsg: "transport failure",
		},
		{
			name:    "5xx retryable → warning, not mismatch",
			client:  &fakeClient{},
			caps:    config.Capabilities{ConfigAPI: true},
			probes:  map[string]capabilityProbe{"config_api": retryableProbe},
			wantSev: SeverityWarning,
			wantMsg: "transport failure",
		},
		{
			name:   "mismatched cap wins over transport when both present",
			client: &fakeClient{},
			caps:   config.Capabilities{ConfigAPI: true, Ring: true},
			probes: map[string]capabilityProbe{
				"config_api": notFoundProbe,
				"ring":       transportProbe,
			},
			wantSev: SeverityError,
			wantMsg: "capability mismatch",
		},
		{
			name:    "missing probe registration → error",
			client:  &fakeClient{},
			caps:    config.Capabilities{ConfigAPI: true},
			probes:  map[string]capabilityProbe{},
			wantSev: SeverityError,
			wantMsg: "no probe registered",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch := CapabilitiesChecker{probes: tc.probes}
			got := ch.Run(t.Context(), config.Backend{Name: "x", Capabilities: tc.caps}, tc.client)
			require.Equal(t, tc.wantSev, got.Severity)
			require.Contains(t, got.Message, tc.wantMsg)
		})
	}
}

func TestClockSkewChecker(t *testing.T) {
	t.Parallel()

	localNow := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		client  backend.Client
		wantSev Severity
		wantMsg string
	}{
		{
			name:    "nil client → error",
			client:  nil,
			wantSev: SeverityError,
			wantMsg: "client construction failed",
		},
		{
			name:    "exactly 30s → ok",
			client:  &fakeClient{probeAtTime: localNow.Add(30 * time.Second)},
			wantSev: SeverityOK,
			wantMsg: "within 30s threshold",
		},
		{
			name:    "31s ahead → warning",
			client:  &fakeClient{probeAtTime: localNow.Add(31 * time.Second)},
			wantSev: SeverityWarning,
			wantMsg: "ahead of local clock",
		},
		{
			name:    "1m behind → warning",
			client:  &fakeClient{probeAtTime: localNow.Add(-1 * time.Minute)},
			wantSev: SeverityWarning,
			wantMsg: "behind local clock",
		},
		{
			name:    "wrapped no-date-header → ok skipped",
			client:  &fakeClient{probeAtErr: errors.Join(backend.ErrNoDateHeader, errors.New("server stripped header"))},
			wantSev: SeverityOK,
			wantMsg: "skipped",
		},
		{
			name:    "transport failure → warning",
			client:  &fakeClient{probeAtErr: errors.New("connection refused")},
			wantSev: SeverityWarning,
			wantMsg: "probe failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch := ClockSkewChecker{now: fixedNow(localNow)}
			got := ch.Run(t.Context(), config.Backend{Name: "x"}, tc.client)
			require.Equal(t, tc.wantSev, got.Severity)
			require.Contains(t, got.Message, tc.wantMsg)
		})
	}
}

// TestClockSkewChecker_NoProberInterface covers the rare path
// where a backend.Client implementation does not satisfy the
// Prober interface — e.g. a future test fake added without the
// ProbeReadyAt method. Without this guard, the type assertion
// would panic at runtime.
func TestClockSkewChecker_NoProberInterface(t *testing.T) {
	t.Parallel()
	got := ClockSkewChecker{}.Run(t.Context(), config.Backend{Name: "x"}, nonProberClient{})
	require.Equal(t, SeverityWarning, got.Severity)
	require.Contains(t, got.Message, "Prober")
}

// nonProberClient implements backend.Client without the Prober
// methods (well, it's a stub: the real backend.Client surface needs
// every method, so we satisfy it via embedded compositional zero —
// see assertion below). Used only to drive the "client doesn't
// satisfy Prober" branch in ClockSkewChecker.
type nonProberClient struct{}

func (nonProberClient) Status(context.Context) (backend.Status, error) {
	return backend.Status{}, nil
}

func (nonProberClient) ListAlerts(context.Context, backend.AlertFilter) ([]backend.Alert, error) {
	return nil, nil
}

func (nonProberClient) ListAlertGroups(context.Context, backend.AlertFilter) ([]backend.AlertGroup, error) {
	return nil, nil
}

func (nonProberClient) ListSilences(context.Context, backend.SilenceFilter) ([]backend.Silence, error) {
	return nil, nil
}

func (nonProberClient) GetSilence(context.Context, string) (backend.Silence, error) {
	return backend.Silence{}, nil
}

func (nonProberClient) ListReceivers(context.Context) ([]backend.Receiver, error) { return nil, nil }
func (nonProberClient) CreateSilence(context.Context, backend.SilenceSpec) (string, error) {
	return "", nil
}
func (nonProberClient) UpdateSilence(context.Context, string, backend.SilenceSpec) error { return nil }
func (nonProberClient) ExpireSilence(context.Context, string) error                      { return nil }
func (nonProberClient) GetConfig(context.Context) (backend.MimirConfig, error) {
	return backend.MimirConfig{}, nil
}
func (nonProberClient) SetConfig(context.Context, backend.MimirConfig) error { return nil }
func (nonProberClient) DeleteConfig(context.Context) error                   { return nil }
func (nonProberClient) ListTenantConfigs(context.Context) ([]backend.TenantConfig, error) {
	return nil, nil
}
func (nonProberClient) RingStatus(context.Context) (backend.Ring, error) { return backend.Ring{}, nil }
func (nonProberClient) Capabilities() backend.Caps                       { return backend.Caps{} }

var _ backend.Client = nonProberClient{}
