// SPDX-License-Identifier: Apache-2.0

// Package mimir constructs a backend.Client configured for Grafana
// Mimir's prefixed Alertmanager surface. v0.1 ships no Mimir-
// specific code beyond the constructor — the prefix is handled by
// vanilla.Client's URL builder and the tenant header by the
// transport layer (per audit §5.1's "one code path per method"
// rule). The package boundary exists so the post-v0.1 config editor
// (Mimir-only per A1) lands in a focused location rather than
// growing vanilla's surface.
package mimir

import (
	"net/http"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/transport"
	"github.com/wilfriedroset/a10r/internal/backend/vanilla"
	"github.com/wilfriedroset/a10r/internal/config"
)

// ClientConfig is the constructor input for New. Mimir-specific
// fields (Prefix, TenantHeader, Tenant) are flat on the struct; auth
// flows through the same *config.AuthSpec the vanilla path uses.
//
//   - BaseURL: required.
//   - Prefix: typically "/alertmanager"; can be customised via
//     Mimir's `-http.alertmanager-http-prefix` flag (audit §2.1).
//   - TenantHeader: typically "X-Scope-OrgID". Empty disables tenant
//     injection — the right shape for Mimir running with
//     `-auth.multitenancy-enabled=false` per audit §2.2.
//   - Tenant: the value sent in TenantHeader.
//   - Auth: optional auth spec applied at the inner transport layer.
//   - Transport: optional base transport (tests inject this; nil
//     defaults to http.DefaultTransport).
//   - Caps: capability flags from `a10r.yaml`. v0.1 leaves these as
//     hints to the TUI; the methods themselves still return
//     ErrUnsupported until the post-v0.1 config editor lands.
type ClientConfig struct {
	BaseURL      string
	Prefix       string
	TenantHeader string
	Tenant       string
	Auth         *config.AuthSpec
	Transport    http.RoundTripper
	Caps         backend.Caps
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
		base = http.DefaultTransport
	}

	authedRT, err := transport.New(cfg.Auth, base)
	if err != nil {
		return nil, err
	}
	tenantedRT := transport.WithTenantHeader(authedRT, cfg.TenantHeader, cfg.Tenant)
	uaRT := transport.WithUserAgent(tenantedRT, cfg.UserAgent)

	return vanilla.New(vanilla.ClientConfig{
		BaseURL:   cfg.BaseURL,
		Prefix:    cfg.Prefix,
		Transport: uaRT,
		Caps:      cfg.Caps,
	})
}
