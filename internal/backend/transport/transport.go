// SPDX-License-Identifier: Apache-2.0

// Package transport composes http.RoundTripper layers for backend
// auth and tenant-header injection. The split into a transport
// package (rather than baking into the vanilla / Mimir clients)
// reflects the audit's design that auth and tenant scoping are
// orthogonal concerns: every backend type uses the same auth shapes
// (basic / bearer / header — mTLS and SigV4 deferred per F2/F3),
// and Mimir only differs from vanilla in needing the tenant header
// injected.
package transport

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/wilfriedroset/a10r/internal/config"
)

// Header names used by built-in auth types. The literal strings are
// also wire-level, so renaming a constant is a wire break — pinned
// in the test suite.
const (
	headerAuthorization = "Authorization"
	bearerPrefix        = "Bearer "
)

// Errors returned by New for malformed AuthSpec inputs. Validation
// is eager so the factory (#15) surfaces the misconfiguration at
// startup rather than on the first poll.
var (
	ErrMissingBasicCreds  = errors.New("auth.type=basic requires auth.basic.{username,password}")
	ErrMissingBearerToken = errors.New("auth.type=bearer requires auth.bearer.token")
	ErrMissingHeaderPair  = errors.New("auth.type=header requires auth.header.{name,value}")
	ErrUnsupportedType    = errors.New("auth.type is not supported in v0.1 (see open-questions F2/F3)")
)

// New returns a RoundTripper that wraps base with the auth scheme
// described in spec. A nil or empty spec / Type=="" / Type=="none"
// short-circuits to base unchanged.
//
// nil base defaults to http.DefaultTransport — keeps test wiring
// terse and matches the stdlib convention used by http.Client.
//
// Returns one of the Err* sentinels for malformed input so the
// factory can render a precise error to the user.
func New(spec *config.AuthSpec, base http.RoundTripper) (http.RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	if spec == nil || spec.Type == "" || spec.Type == config.AuthTypeNone {
		return base, nil
	}

	switch spec.Type {
	case config.AuthTypeBasic:
		return newBasic(spec.Basic, base)
	case config.AuthTypeBearer:
		return newBearer(spec.Bearer, base)
	case config.AuthTypeHeader:
		return newHeader(spec.Header, base)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, spec.Type)
	}
}

// WithTenantHeader wraps base in a RoundTripper that injects the
// (name, value) pair on every request before delegating. Composes
// with auth: chain `WithTenantHeader(New(spec, base), header, value)`
// so auth is the inner layer (visible to base) and the tenant header
// is added on top.
//
// An empty name short-circuits to base unchanged so callers can
// pass the config-flat value without conditional plumbing.
func WithTenantHeader(base http.RoundTripper, name, value string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if name == "" {
		return base
	}
	return &addHeaderRT{base: base, name: name, value: value}
}

func newBasic(spec *config.BasicAuth, base http.RoundTripper) (http.RoundTripper, error) {
	if spec == nil || spec.Username == "" || spec.Password == "" {
		return nil, ErrMissingBasicCreds
	}
	return &basicRT{base: base, user: spec.Username, pass: spec.Password}, nil
}

func newBearer(spec *config.BearerAuth, base http.RoundTripper) (http.RoundTripper, error) {
	if spec == nil || spec.Token == "" {
		return nil, ErrMissingBearerToken
	}
	return &addHeaderRT{base: base, name: headerAuthorization, value: bearerPrefix + spec.Token}, nil
}

func newHeader(spec *config.HeaderAuth, base http.RoundTripper) (http.RoundTripper, error) {
	if spec == nil || spec.Name == "" || spec.Value == "" {
		return nil, ErrMissingHeaderPair
	}
	return &addHeaderRT{base: base, name: spec.Name, value: spec.Value}, nil
}

// basicRT injects HTTP Basic auth via http.Request.SetBasicAuth so
// the base64 encoding lives in stdlib rather than this package.
type basicRT struct {
	base       http.RoundTripper
	user, pass string
}

func (rt *basicRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.SetBasicAuth(rt.user, rt.pass)
	return rt.base.RoundTrip(cloned) //nolint:wrapcheck // RoundTripper contract: errors propagate as-is
}

// addHeaderRT sets a single (name, value) header on every request.
// Used both for bearer auth (with name=Authorization, value=Bearer
// <token>) and for raw header auth and for tenant header injection.
type addHeaderRT struct {
	base        http.RoundTripper
	name, value string
}

func (rt *addHeaderRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set(rt.name, rt.value)
	return rt.base.RoundTrip(cloned) //nolint:wrapcheck // RoundTripper contract: errors propagate as-is
}
