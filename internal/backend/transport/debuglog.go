// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"cmp"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
)

// WithDebugLog logs every request/response pair as slog attrs at
// LevelDebug; nil log short-circuits to base unchanged.
//
// Header values are emitted raw — secret-bearing keys are masked at
// slog write time by internal/log's ReplaceAttr hook (ADR 0008). Slot
// innermost (between NewBase and NewAuth) so the log captures the final
// request after every upstream layer has injected its bits. Bodies are
// not captured: open-ended content makes a scrub layer more risk than
// value (ADR 0008).
func WithDebugLog(base http.RoundTripper, log *slog.Logger) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if log == nil {
		return base
	}
	return &debugLogRT{base: base, log: log}
}

type debugLogRT struct {
	base http.RoundTripper
	log  *slog.Logger
}

// RoundTrip emits the log line after the response (or error) so latency
// and status share one record; errors still log at LevelDebug with
// status=0. req.Context is forwarded to LogAttrs for handler-side
// request-scoped extraction (trace IDs), not for cancellation.
func (r *debugLogRT) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := r.base.RoundTrip(req)
	latency := time.Since(start)

	if err != nil {
		r.logError(req, latency, err)
		return resp, err //nolint:wrapcheck // RoundTripper contract requires errors propagate as-is.
	}
	r.logSuccess(req, resp, latency)
	return resp, nil
}

func (r *debugLogRT) logSuccess(req *http.Request, resp *http.Response, latency time.Duration) {
	r.log.LogAttrs(req.Context(), slog.LevelDebug, "http",
		slog.String("method", req.Method),
		slog.String("url", req.URL.String()),
		slog.Duration("latency", latency),
		headersGroup("req_headers", req.Header),
		slog.Int("status", resp.StatusCode),
		headersGroup("resp_headers", resp.Header),
	)
}

func (r *debugLogRT) logError(req *http.Request, latency time.Duration, err error) {
	r.log.LogAttrs(req.Context(), slog.LevelDebug, "http",
		slog.String("method", req.Method),
		slog.String("url", req.URL.String()),
		slog.Duration("latency", latency),
		headersGroup("req_headers", req.Header),
		slog.Int("status", 0),
		slog.String("error", err.Error()),
	)
}

// headersGroup flattens an http.Header into a slog.Group of nested
// attrs that each pass through ReplaceAttr individually, so
// Authorization and friends emerge as ***. The direct range avoids an
// http.CanonicalHeaderKey round-trip that would silently drop
// non-canonical-keyed entries. Keys are sorted for deterministic tests.
func headersGroup(label string, h http.Header) slog.Attr {
	type kv struct {
		key string
		val string
	}
	pairs := make([]kv, 0, len(h))
	for k, v := range h {
		pairs = append(pairs, kv{key: strings.ToLower(k), val: strings.Join(v, ", ")})
	}
	slices.SortFunc(pairs, func(a, b kv) int { return cmp.Compare(a.key, b.key) })

	args := make([]any, 0, len(pairs))
	for _, p := range pairs {
		args = append(args, slog.String(p.key, p.val))
	}
	return slog.Group(label, args...)
}
