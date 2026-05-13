// SPDX-License-Identifier: Apache-2.0

package log

import (
	"context"
	"log/slog"
	"regexp"
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
	"passwd":              {},
	"token":               {},
	"bearer":              {},
	"credentials":         {},
	"api-key":             {},
	"x-api-key":           {},
	"secret":              {},
	"client-secret":       {},
	"client_secret":       {},
	"access-token":        {},
	"access_token":        {},
	"refresh-token":       {},
	"refresh_token":       {},
	"private-key":         {},
	"private_key":         {},
	"session":             {},
	"sessionid":           {},
	"csrf":                {},
}

// urlUserinfoRE matches a URL scheme + user:password@host segment so
// the userinfo can be stripped. Caters to http(s), grpc, redis, ws,
// and anything else conforming to RFC 3986 — the leading scheme is
// required so we don't false-positive on email-like `name@host`
// substrings (those don't carry the `://` prefix). Password is
// optional so `https://user@host` is also caught.
var urlUserinfoRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

// stripURLUserinfo removes embedded credentials from any URL substring
// in s. Common leak vector: a backend client returns an error wrapping
// its request URL ("dial tcp https://user:pass@host: connection
// refused"), the caller logs the error verbatim, and the password is
// in the log file. Inputs without a URL pass through unchanged.
func stripURLUserinfo(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	return urlUserinfoRE.ReplaceAllString(s, "$1")
}

// redactAttr is the slog.HandlerOptions.ReplaceAttr hook. It masks
// any attribute whose key matches secretKeys (case-insensitive),
// passing other attributes through unchanged. Group prefixes are
// ignored: a `req.authorization` nested attr is masked the same way
// as a top-level `authorization` — slog invokes ReplaceAttr per
// nested attr with the group path supplied separately.
//
// String attr values are additionally scanned for embedded URL
// userinfo so a value like "https://alice:hunter2@host" never lands
// in the log file even when the attr key (e.g. "url") isn't itself
// a secret key.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if _, ok := secretKeys[strings.ToLower(a.Key)]; ok {
		return slog.String(a.Key, marker)
	}
	if a.Value.Kind() == slog.KindString {
		stripped := stripURLUserinfo(a.Value.String())
		if stripped != a.Value.String() {
			return slog.String(a.Key, stripped)
		}
	}
	return a
}

// msgRedactingHandler wraps a slog.Handler and strips URL userinfo
// from the record message before delegating. Closes the leak class
// where a wrapped error or hand-formatted log line carries a credential
// in a URL: slog's ReplaceAttr never sees record.Message, so without
// this wrapper, `slog.Info("connect failed: " + url)` would land
// verbatim in the file.
type msgRedactingHandler struct {
	inner slog.Handler
}

// Enabled delegates to the wrapped handler.
func (h *msgRedactingHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

// Handle redacts r.Message in-place (slog.Record is a value type, so
// mutating the local copy is private to this call) before delegating.
func (h *msgRedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Message = stripURLUserinfo(r.Message)
	return h.inner.Handle(ctx, r)
}

// WithAttrs / WithGroup must preserve the wrapper so subsequent
// records also flow through msg redaction.
func (h *msgRedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &msgRedactingHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *msgRedactingHandler) WithGroup(name string) slog.Handler {
	return &msgRedactingHandler{inner: h.inner.WithGroup(name)}
}
