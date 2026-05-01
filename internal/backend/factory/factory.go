// SPDX-License-Identifier: Apache-2.0

// Package factory is the single-entry-point wiring between
// `a10r.yaml`'s `backends:` array and the runtime backend.Client
// implementations. Per audit §5.1 there is one code path per method;
// vanilla Alertmanager is just the Mimir constructor with empty
// prefix and empty tenant header.
//
// The package lives as a sub-package of internal/backend rather than
// inside it because the parent package is imported by both vanilla
// and mimir, and a factory living in the parent would create a cycle
// (backend → mimir → backend).
package factory

import (
	"fmt"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/mimir"
	"github.com/wilfriedroset/a10r/internal/config"
)

// Build constructs a backend.Client from one entry of the user's
// `backends:` array. There is no NewVanilla / NewMimir split — the
// audit deliberately chose a single code path: vanilla means
// "prefix is empty and no tenant header"; Mimir is the same
// constructor with prefix and (optionally) tenant header set.
//
// Validation happens eagerly so a misconfigured backend surfaces at
// startup rather than on the first poll. The wrapped error always
// carries the backend's Name so multi-backend setups know which
// entry failed.
//
// userAgent is the RFC 9110 User-Agent string applied to every
// outgoing HTTP request. Pass an empty string to disable injection
// (tests do; production callers should always pass a meaningful
// value built from the cmd build vars).
func Build(cfg config.Backend, userAgent string) (backend.Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("backend %q: url is required", cfg.Name)
	}

	c, err := mimir.New(mimir.ClientConfig{
		BaseURL:      cfg.URL,
		Prefix:       cfg.Prefix,
		TenantHeader: cfg.TenantHeader,
		Tenant:       cfg.Tenant,
		Auth:         cfg.Auth,
		Caps:         cfg.Capabilities,
		UserAgent:    userAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("backend %q: %w", cfg.Name, err)
	}
	return c, nil
}
