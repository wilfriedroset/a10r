// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	a10rlog "github.com/wilfriedroset/a10r/internal/log"
)

// stubRT is an inline RoundTripper that returns a configured
// response/error without going to the network. Used for the
// transport-error path where httptest.NewServer can't model
// "connection refused".
type stubRT struct {
	resp *http.Response
	err  error
}

func (s *stubRT) RoundTrip(*http.Request) (*http.Response, error) {
	return s.resp, s.err
}

// debugCapturingLogger returns a slog.Logger writing into buf at
// Debug level with no ReplaceAttr — these tests want to see the
// raw attrs WithDebugLog emits. Redaction is exercised end-to-end
// in TestWithDebugLog_RedactionEndToEnd via a10rlog.New.
func debugCapturingLogger(buf io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestWithDebugLog_NilLogShortCircuits(t *testing.T) {
	t.Parallel()

	base := &stubRT{resp: &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}}
	got := WithDebugLog(base, nil)
	require.Same(t, base, got, "nil log returns base unchanged")
}

func TestWithDebugLog_NilBaseDefaultsToHTTPDefaultTransport(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	got := WithDebugLog(nil, debugCapturingLogger(&buf))
	rt, ok := got.(*debugLogRT)
	require.True(t, ok, "non-nil log returns the wrapper struct")
	require.Equal(t, http.DefaultTransport, rt.base, "nil base defaults to DefaultTransport")
}

func TestWithDebugLog_LogsRequestMetadata(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Trace-Id", "abc")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	var buf bytes.Buffer
	rt := WithDebugLog(http.DefaultTransport, debugCapturingLogger(&buf))
	client := &http.Client{Transport: rt}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v2/status", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Scope-Orgid", "tenant-a")
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	out := buf.String()
	require.Contains(t, out, "level=DEBUG")
	require.Contains(t, out, "msg=http")
	require.Contains(t, out, "method=GET")
	require.Contains(t, out, "/api/v2/status")
	require.Contains(t, out, "status=200")
	require.Contains(t, out, "latency=")
	require.Contains(t, out, "req_headers.x-scope-orgid=tenant-a")
	require.Contains(t, out, "resp_headers.x-trace-id=abc")
}

func TestWithDebugLog_LogsTransportError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("connection refused")
	stub := &stubRT{err: wantErr}
	var buf bytes.Buffer
	rt := WithDebugLog(stub, debugCapturingLogger(&buf))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://nope.example/", http.NoBody)
	require.NoError(t, err)
	_, err = rt.RoundTrip(req) //nolint:bodyclose // stubRT returns (nil, err); no body to close
	require.ErrorIs(t, err, wantErr, "wrapper returns base error unchanged")

	out := buf.String()
	require.Contains(t, out, "status=0", "error path emits status=0")
	require.Contains(t, out, `error="connection refused"`)
}

// TestWithDebugLog_RedactionEndToEnd proves the full chain:
// WithDebugLog -> a10rlog.New (with ReplaceAttr) -> file. The
// secret-bearing Authorization header must emerge masked, while
// the X-Scope-OrgID routing key must pass through unmasked.
func TestWithDebugLog_RedactionEndToEnd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a10r.log")
	logger, closer, err := a10rlog.New(a10rlog.Opts{
		Format: a10rlog.FormatLogfmt,
		Level:  slog.LevelDebug,
		Path:   path,
	})
	require.NoError(t, err)
	// closer.Close is invoked explicitly below before reading the
	// log file (lumberjack documents Close as the flush boundary).
	// No t.Cleanup-side close: lumberjack's Close is idempotent but
	// the second invocation only confuses test intent.

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	rt := WithDebugLog(http.DefaultTransport, logger)
	client := &http.Client{Transport: rt}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-Scope-Orgid", "tenant-a")

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, closer.Close())

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	out := string(body)
	require.Contains(t, out, "req_headers.authorization=***", "credential masked")
	require.NotContains(t, out, "secret-token", "raw token never appears")
	require.Contains(t, out, "req_headers.x-scope-orgid=tenant-a",
		"routing key passes through unmasked")
}
