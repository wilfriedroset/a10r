// SPDX-License-Identifier: Apache-2.0

// Package factory is the single-entry-point wiring between
// `a10r.yaml`'s `backends:` array and the runtime backend.Client
// implementations. Per audit §5.1 there is one code path per method;
// vanilla Alertmanager is just the Mimir constructor with empty
// prefix and empty Headers map.
//
// The package lives as a sub-package of internal/backend rather than
// inside it because the parent package is imported by both vanilla
// and mimir, and a factory living in the parent would create a cycle
// (backend → mimir → backend).
package factory

import (
	"fmt"
	"log/slog"
	"maps"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/mimir"
	"github.com/wilfriedroset/a10r/internal/config"
)

// Option mutates Build's optional configuration. Functional options
// keep the existing positional signature stable for the many test
// callers while letting new behaviour layer in additively.
type Option func(*options)

// options collects every Build optional field. Zero value is
// valid: every field is "do nothing" by default.
type options struct {
	debugLog *slog.Logger
}

// WithDebugLog wires per-request HTTP debug logging via
// transport.WithDebugLog on the resulting client. Pass the
// production *slog.Logger only when --debug-http is set; nil-safe.
// Header redaction comes from the logger's own ReplaceAttr hook
// (ADR 0008) — this option just enables the capture.
func WithDebugLog(log *slog.Logger) Option {
	return func(o *options) { o.debugLog = log }
}

// Build constructs a backend.Client from one entry of the user's
// `backends:` array. There is no NewVanilla / NewMimir split — the
// audit deliberately chose a single code path: vanilla means
// "prefix is empty and no Headers"; Mimir is the same constructor
// with prefix and (optionally) tenant header set.
//
// The factory folds the YAML `tenant_header:` / `tenant:` sugar
// into the same Headers map that arbitrary user-supplied headers
// land in, so downstream code sees one shape.
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
func Build(cfg config.Backend, userAgent string, opts ...Option) (backend.Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("backend %q: url is required", cfg.Name)
	}

	o := options{}
	for _, apply := range opts {
		apply(&o)
	}

	c, err := mimir.New(mimir.ClientConfig{
		BaseURL:              cfg.URL,
		Prefix:               cfg.Prefix,
		Headers:              mergedHeaders(cfg),
		BasicAuth:            cfg.BasicAuth,
		Authorization:        cfg.Authorization,
		BearerToken:          cfg.BearerToken,
		TLS:                  cfg.TLSConfig,
		ProxyURL:             cfg.ProxyURL,
		NoProxy:              cfg.NoProxy,
		ProxyFromEnvironment: cfg.ProxyFromEnvironment,
		Timeout:              cfg.RemoteTimeout,
		Caps:                 cfg.Capabilities,
		UserAgent:            userAgent,
		DebugLog:             o.debugLog,
	})
	if err != nil {
		return nil, fmt.Errorf("backend %q: %w", cfg.Name, err)
	}
	return c, nil
}

// mergedHeaders returns a single map combining the user's Headers
// block with the tenant_header / tenant YAML sugar. Returns nil
// when neither source contributes anything so transport.WithHeaders
// short-circuits and no allocation happens for the common
// no-headers case.
//
// The schema layer (config.Backend.Validate) guarantees these two
// sources do not collide on the same header name, so the merge is
// unambiguous.
func mergedHeaders(cfg config.Backend) map[string]string {
	if len(cfg.Headers) == 0 && cfg.TenantHeader == "" {
		return nil
	}
	out := make(map[string]string, len(cfg.Headers)+1)
	maps.Copy(out, cfg.Headers)
	if cfg.TenantHeader != "" {
		out[cfg.TenantHeader] = cfg.Tenant
	}
	return out
}
