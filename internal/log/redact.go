// SPDX-License-Identifier: Apache-2.0

package log

import (
	"log/slog"
	"strings"
)

// marker is the placeholder substituted for redacted secret values.
// Matches internal/tui/page/tenantconfig's existing convention so a
// single visual signal ("***") tells the operator "redacted" across
// every surface a10r writes secrets onto. The two constants are
// defined separately to keep this package free of TUI deps; lift
// into a shared package if a third surface adopts the marker.
const marker = "***"

// secretKeys is the fixed lowercase set of slog attribute keys whose
// values are masked by redactAttr. Per ADR 0008 the set is closed —
// not user-extensible — to avoid misconfig where an operator
// accidentally redacts a key they need (or fails to redact one they
// don't realise carries a secret). Do NOT mutate at runtime; the
// redact_test.go suite asserts the membership list as a regression
// guard against accidental drift.
//
// X-Scope-OrgID is intentionally absent: it is a Mimir routing
// identifier (the tenant ID), not a credential, and multi-tenant
// debug becomes meaningless when every tenant's request looks
// identical. See ADR 0008.
//
// False-positive note: callers logging an opaque-but-not-secret
// "token" (e.g. a pagination cursor, an idempotency token) will
// see it masked. Prefer attr keys like `cursor`, `next_page`,
// `request_id`, or `idempotency_key` for non-secret values so they
// stay readable in the log file.
var secretKeys = map[string]struct{}{
	"authorization":       {},
	"cookie":              {},
	"set-cookie":          {},
	"proxy-authorization": {},
	"password":            {},
	"token":               {},
	"bearer":              {},
	"credentials":         {},
	"api-key":             {},
	"x-api-key":           {},
}

// redactAttr is the slog.HandlerOptions.ReplaceAttr hook. It masks
// any attribute whose key matches secretKeys (case-insensitive),
// passing other attributes through unchanged. Group prefixes are
// ignored: a `req.authorization` nested attr is masked the same way
// as a top-level `authorization` — slog invokes ReplaceAttr per
// nested attr with the group path supplied separately.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if _, ok := secretKeys[strings.ToLower(a.Key)]; !ok {
		return a
	}
	return slog.String(a.Key, marker)
}
