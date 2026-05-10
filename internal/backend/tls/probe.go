// SPDX-License-Identifier: Apache-2.0

// Package tls provides a tiny standalone TLS-handshake helper used
// by `a10r doctor`'s TLS-expiry check. It is deliberately separate
// from internal/backend/transport: the doctor probe must NOT reach
// into the constructed http.Client's transport (the auth /
// host-pinning chain is opaque from the outside) and must NOT
// require building a full backend.Client just to read the leaf
// certificate. ProbeCert opens a fresh TCP+TLS connection, reads
// the peer's leaf cert, and closes the connection — nothing more.
//
// v0.0.1 scope:
//   - HTTP_PROXY / HTTPS_PROXY environment variables are NOT
//     honoured. A backend reachable only through a corporate proxy
//     surfaces ProbeCert as ErrUnreachable and the operator falls
//     back to whatever cert-monitoring tooling already exists in
//     that environment.
//   - The check runs against the URL's host:port directly via
//     tls.Dial; no SNI override beyond the URL's host name.
package tls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
)

// ErrNotHTTPS is returned by ProbeCert when the supplied URL has a
// scheme other than `https`. The doctor check converts this into a
// SeverityOK ("N/A") result rather than a warning — http:// is a
// supported configuration and a TLS probe simply does not apply.
var ErrNotHTTPS = errors.New("not an https url")

// Dialer abstracts the network+TLS handshake so tests can inject an
// in-memory tls.Conn without going through the real network stack.
// The single method mirrors tls.Dialer.DialContext's signature plus
// an explicit *tls.Config so ProbeCert can pass the per-URL config
// without mutating the dialer.
//
// Production callers leave Dialer nil; ProbeCert defaults to a
// tls.Dialer using the package default (no NetDialer override).
type Dialer interface {
	DialContext(ctx context.Context, network, addr string, cfg *tls.Config) (*tls.Conn, error)
}

// ProbeCert opens a TLS connection to the host portion of rawURL,
// reads the leaf certificate, and returns it. Closes the connection
// before returning regardless of success — the caller's only
// concern is the cert.
//
// Returns ErrNotHTTPS for non-https schemes (so the caller can
// short-circuit). Returns a wrapped error for parse failures, dial
// failures, and TLS handshake failures.
//
// dialer is the seam for tests; nil defers to the package default.
func ProbeCert(ctx context.Context, rawURL string, dialer Dialer) (*x509.Certificate, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme %q", ErrNotHTTPS, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("url %q has no host", rawURL)
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	if dialer == nil {
		dialer = NewDefaultDialer()
	}
	cfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	conn, err := dialer.DialContext(ctx, "tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("tls dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("tls dial %s: server returned no peer certificates", addr)
	}
	return state.PeerCertificates[0], nil
}

// defaultDialer is the production Dialer. tls.Dialer carries its
// config in the struct, so DialContext rebuilds a per-call dialer
// with the supplied cfg rather than mutating shared state.
type defaultDialer struct{}

// DialContext implements Dialer.
func (defaultDialer) DialContext(ctx context.Context, network, addr string, cfg *tls.Config) (*tls.Conn, error) {
	d := tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err //nolint:wrapcheck // surfaced through ProbeCert's wrap
	}
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		// tls.Dialer.DialContext is documented to return a *tls.Conn on
		// success; this branch exists so a future stdlib contract
		// change surfaces as a clear error rather than a runtime panic.
		_ = conn.Close()
		return nil, fmt.Errorf("tls dialer returned %T, want *tls.Conn", conn)
	}
	return tlsConn, nil
}

// NewDefaultDialer returns the production Dialer used when ProbeCert
// is called with nil. Exposed for symmetry with the test seam — most
// callers pass nil and never see this constructor.
func NewDefaultDialer() Dialer { return defaultDialer{} }
