// SPDX-License-Identifier: Apache-2.0

// Package mimir constructs a backend.Client configured for Grafana
// Mimir's prefixed Alertmanager surface. v0.1 ships no Mimir-
// specific code beyond the constructor — the prefix is handled by
// vanilla.Client's URL builder and the per-tenant header by the
// Headers map injected via transport.WithHeaders (per audit §5.1's
// "one code path per method" rule). The package boundary exists so
// the post-v0.1 config editor (Mimir-only per A1) lands in a focused
// location rather than growing vanilla's surface.
package mimir

import (
	"net/http"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/transport"
	"github.com/wilfriedroset/a10r/internal/backend/vanilla"
	"github.com/wilfriedroset/a10r/internal/config"
)

// ClientConfig is the constructor input for New. Mimir-specific
// fields (Prefix, Headers) are flat on the struct; auth flows
// through the same peers (BasicAuth / Authorization / BearerToken)
// the vanilla path uses, matching Prometheus's `remote_write` shape.
//
//   - BaseURL: required.
//   - Prefix: typically "/alertmanager"; can be customised via
//     Mimir's `-http.alertmanager-http-prefix` flag (audit §2.1).
//   - Headers: arbitrary per-request headers, including the tenant
//     header (X-Scope-OrgID) when Mimir is multi-tenant. The
//     factory layer folds the tenant_header / tenant sugar into
//     this map at construction time.
//   - Auth: at most one of BasicAuth, Authorization, BearerToken.
//   - TLS / ProxyURL / NoProxy / ProxyFromEnvironment: forwarded to
//     transport.NewBase.
//   - Timeout: per-request timeout. Zero defers to
//     vanilla.Client's default.
//   - Caps: capability flags from `a10r.yaml`. v0.1 leaves these as
//     hints to the TUI; the methods themselves still return
//     ErrUnsupported until the post-v0.1 config editor lands.
type ClientConfig struct {
	BaseURL string
	Prefix  string
	Headers map[string]string

	BasicAuth     *config.BasicAuth
	Authorization *config.Authorization
	BearerToken   string

	TLS                  *config.TLSConfig
	ProxyURL             string
	NoProxy              string
	ProxyFromEnvironment bool

	Timeout time.Duration

	// Transport, when non-nil, replaces the *http.Transport the
	// constructor would otherwise build via transport.NewBase. Tests
	// inject this to short-circuit TLS / proxy configuration; production
	// callers leave it nil.
	Transport http.RoundTripper

	Caps backend.Caps

	// UserAgent is the value sent in the HTTP User-Agent header on
	// every request. Empty disables injection. Composed outside auth
	// and tenant layers so a downstream proxy that strips Authorization
	// still sees the identifying UA.
	UserAgent string
}

// New constructs a *vanilla.Client wrapped with the Mimir-specific
// transport layers. Returns *vanilla.Client (rather than a
// dedicated *mimir.Client) because v0.1 Mimir adds no behaviour
// over vanilla — the audit (§5.1) explicitly chose "one code path
// per method". The post-v0.1 config editor will introduce a
// dedicated type that embeds or wraps vanilla.Client and overrides
// the capability stubs.
func New(cfg ClientConfig) (*vanilla.Client, error) {
	base := cfg.Transport
	if base == nil {
		built, err := transport.NewBase(transport.BaseOptions{
			TLS:                  cfg.TLS,
			ProxyURL:             cfg.ProxyURL,
			NoProxy:              cfg.NoProxy,
			ProxyFromEnvironment: cfg.ProxyFromEnvironment,
		})
		if err != nil {
			return nil, err
		}
		base = built
	}

	authedRT, err := transport.NewAuth(transport.AuthOptions{
		BasicAuth:     cfg.BasicAuth,
		Authorization: cfg.Authorization,
		BearerToken:   cfg.BearerToken,
	}, base)
	if err != nil {
		return nil, err
	}
	headeredRT := transport.WithHeaders(authedRT, cfg.Headers)
	uaRT := transport.WithUserAgent(headeredRT, cfg.UserAgent)

	return vanilla.New(vanilla.ClientConfig{
		BaseURL:   cfg.BaseURL,
		Prefix:    cfg.Prefix,
		Transport: uaRT,
		Timeout:   cfg.Timeout,
		Caps:      cfg.Caps,
	})
}
