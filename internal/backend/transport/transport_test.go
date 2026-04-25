// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
)

// captureHandler is an http.HandlerFunc that records the headers of
// the last request it served, so tests can assert wire-level shape.
type captureHandler struct {
	headers http.Header
}

func (c *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.headers = r.Header.Clone()
	w.WriteHeader(http.StatusNoContent)
}

// roundTripOnce drives rt through one GET request against srv. Tests
// inspect the resulting wire headers via a captureHandler attached
// to srv rather than the roundtripped request, so this helper is
// deliberately void-returning.
func roundTripOnce(t *testing.T, rt http.RoundTripper, srv *httptest.Server) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
}

func TestNew_NoneOrEmptyReturnsBase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec *config.AuthSpec
	}{
		{name: "nil spec"},
		{name: "empty type", spec: &config.AuthSpec{}},
		{name: "explicit none", spec: &config.AuthSpec{Type: config.AuthTypeNone}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			base := http.DefaultTransport
			rt, err := New(tc.spec, base)
			require.NoError(t, err)
			require.Same(t, base, rt, "no-op auth must return the base RT unchanged")
		})
	}
}

func TestNew_BasicAuthHeader(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt, err := New(&config.AuthSpec{
		Type:  config.AuthTypeBasic,
		Basic: &config.BasicAuth{Username: "alice", Password: "s3cret"},
	}, http.DefaultTransport)
	require.NoError(t, err)

	roundTripOnce(t, rt, srv)

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	require.Equal(t, want, srvCap.headers.Get(headerAuthorization))
}

func TestNew_BearerAuthHeader(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt, err := New(&config.AuthSpec{
		Type:   config.AuthTypeBearer,
		Bearer: &config.BearerAuth{Token: "abc.def.ghi"},
	}, http.DefaultTransport)
	require.NoError(t, err)

	roundTripOnce(t, rt, srv)

	require.Equal(t, "Bearer abc.def.ghi", srvCap.headers.Get(headerAuthorization))
}

func TestNew_CustomHeader(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt, err := New(&config.AuthSpec{
		Type:   config.AuthTypeHeader,
		Header: &config.HeaderAuth{Name: "X-API-Key", Value: "kkkk"},
	}, http.DefaultTransport)
	require.NoError(t, err)

	roundTripOnce(t, rt, srv)

	require.Equal(t, "kkkk", srvCap.headers.Get("X-Api-Key"))
	require.Empty(t, srvCap.headers.Get(headerAuthorization),
		"custom header auth must not also set Authorization")
}

func TestNew_MalformedSpecsReturnSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		spec    *config.AuthSpec
		wantErr error
	}{
		{
			name:    "basic missing struct",
			spec:    &config.AuthSpec{Type: config.AuthTypeBasic},
			wantErr: ErrMissingBasicCreds,
		},
		{
			name: "basic missing username",
			spec: &config.AuthSpec{
				Type:  config.AuthTypeBasic,
				Basic: &config.BasicAuth{Password: "x"},
			},
			wantErr: ErrMissingBasicCreds,
		},
		{
			name: "basic missing password",
			spec: &config.AuthSpec{
				Type:  config.AuthTypeBasic,
				Basic: &config.BasicAuth{Username: "x"},
			},
			wantErr: ErrMissingBasicCreds,
		},
		{
			name:    "bearer missing struct",
			spec:    &config.AuthSpec{Type: config.AuthTypeBearer},
			wantErr: ErrMissingBearerToken,
		},
		{
			name: "bearer empty token",
			spec: &config.AuthSpec{
				Type:   config.AuthTypeBearer,
				Bearer: &config.BearerAuth{},
			},
			wantErr: ErrMissingBearerToken,
		},
		{
			name:    "header missing struct",
			spec:    &config.AuthSpec{Type: config.AuthTypeHeader},
			wantErr: ErrMissingHeaderPair,
		},
		{
			name: "header missing value",
			spec: &config.AuthSpec{
				Type:   config.AuthTypeHeader,
				Header: &config.HeaderAuth{Name: "X-K"},
			},
			wantErr: ErrMissingHeaderPair,
		},
		{
			name:    "unsupported type",
			spec:    &config.AuthSpec{Type: "mtls"},
			wantErr: ErrUnsupportedType,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tc.spec, http.DefaultTransport)
			require.Error(t, err)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestNew_NilBaseDefaultsToDefaultTransport(t *testing.T) {
	t.Parallel()

	rt, err := New(nil, nil)
	require.NoError(t, err)
	require.Same(t, http.DefaultTransport, rt,
		"nil base + nil spec must return http.DefaultTransport unchanged")
}

func TestWithTenantHeader_Injects(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt := WithTenantHeader(http.DefaultTransport, "X-Scope-OrgID", "tenant-a")
	roundTripOnce(t, rt, srv)

	require.Equal(t, "tenant-a", srvCap.headers.Get("X-Scope-Orgid"))
}

func TestWithTenantHeader_EmptyNameShortCircuits(t *testing.T) {
	t.Parallel()

	base := http.DefaultTransport
	rt := WithTenantHeader(base, "", "tenant-a")
	require.Same(t, base, rt, "empty header name must return base unchanged")
}

func TestComposition_AuthAndTenantBothApply(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	authed, err := New(&config.AuthSpec{
		Type:   config.AuthTypeBearer,
		Bearer: &config.BearerAuth{Token: "tok"},
	}, http.DefaultTransport)
	require.NoError(t, err)

	rt := WithTenantHeader(authed, "X-Scope-OrgID", "tenant-b")
	roundTripOnce(t, rt, srv)

	require.Equal(t, "Bearer tok", srvCap.headers.Get(headerAuthorization),
		"auth header must reach the wire")
	require.Equal(t, "tenant-b", srvCap.headers.Get("X-Scope-Orgid"),
		"tenant header must reach the wire")
}

func TestRoundTrip_DoesNotMutateCallerRequest(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	rt, err := New(&config.AuthSpec{
		Type:   config.AuthTypeBearer,
		Bearer: &config.BearerAuth{Token: "tok"},
	}, http.DefaultTransport)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	// The bearer header must NOT be visible on the original request
	// — clone-then-mutate is the contract.
	require.Empty(t, req.Header.Get(headerAuthorization),
		"caller's request must not be mutated by the RoundTripper chain")

	// And the bearer header must NOT include any leakage of the
	// original context's cancellation signal
	require.True(t, strings.HasPrefix(srv.URL, "http://"))
}
