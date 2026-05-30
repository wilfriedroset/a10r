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

// WithDebugLog wraps base in a RoundTripper that logs every
// request/response pair as structured slog attrs at LevelDebug.
// log==nil short-circuits to base unchanged so callers can pass a
// disabled logger without conditional plumbing.
//
// Header values are emitted raw — secret-bearing keys are masked
// at slog handler-write time by internal/log's ReplaceAttr hook
// (ADR 0008). Slot innermost in the chain (closest to wire),
// between NewBase and NewAuth, so the log captures the final
// request after every upstream layer (auth, headers, user-agent)
// has injected its bits.
//
// No body capture: bodies are open-ended (free-form alert
// annotations, silence comments) and a regex scrub layer is more
// risk than the debugging value warrants — see ADR 0008.
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

// RoundTrip implements http.RoundTripper. The log line is emitted
// after the response (or error) so latency and status are captured
// in the same record. On transport errors, the line still emits at
// LevelDebug with status=0 + an error attr — the caller decides
// whether the error is fatal. The req.Context is forwarded to
// LogAttrs so handler-side request-scoped extraction (trace IDs,
// span info) works; the context is not used for cancellation here.
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

// headersGroup flattens an http.Header into a slog.Group whose
// nested attrs are (lowercase-key, joined-value) pairs. Each
// nested attr passes through ReplaceAttr individually, so
// Authorization and friends emerge as ***.
//
// Multi-valued headers are joined with ", " — RFC 9110 §5.3
// equivalent form, matching what http.Header.Get already collapses
// to. The single-pass range pairs each key with its value slice
// directly, avoiding an http.CanonicalHeaderKey round-trip that
// would silently drop non-canonical-keyed entries. Keys are sorted
// so test output is deterministic.
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
