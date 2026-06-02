// SPDX-License-Identifier: Apache-2.0

package log

import (
	"bytes"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactAttr_MasksEverySecretKey(t *testing.T) {
	t.Parallel()

	for key := range secretKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			got := redactAttr(nil, slog.String(key, "secret-value"))
			require.Equal(t, key, got.Key, "key passes through unchanged")
			require.Equal(t, marker, got.Value.String(), "value masked")
		})
	}
}

func TestRedactAttr_CaseInsensitive(t *testing.T) {
	t.Parallel()

	cases := []string{"Authorization", "AUTHORIZATION", "X-Api-Key", "X-API-KEY", "Cookie"}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			got := redactAttr(nil, slog.String(key, "tok"))
			require.Equal(t, marker, got.Value.String())
		})
	}
}

func TestRedactAttr_PassesThroughNonSecret(t *testing.T) {
	t.Parallel()

	cases := []slog.Attr{
		slog.String("user-agent", "a10r/0.0.1"), //nolint:sloglint // HTTP header names are kebab-case per RFC 9110
		slog.String("method", "GET"),
		slog.Int("status", 200),
		slog.Int64("latency_ms", 42),
		// X-Scope-OrgID must NOT be masked — see ADR 0008.
		slog.String("x-scope-orgid", "tenant-prod"), //nolint:sloglint // HTTP header names are kebab-case per RFC 9110
	}
	for _, a := range cases {
		t.Run(a.Key, func(t *testing.T) {
			t.Parallel()
			got := redactAttr(nil, a)
			require.Equal(t, a.Key, got.Key)
			require.Equal(t, a.Value.String(), got.Value.String(),
				"non-secret attr value passes through unchanged")
		})
	}
}

func TestRedactAttr_NestedGroupKeyStillMasked(t *testing.T) {
	t.Parallel()

	// The group path is supplied to ReplaceAttr but ignored here —
	// only the leaf key is matched against secretKeys. Handler-level
	// grouping prefixes the rendered output ("req.authorization=…")
	// at write time, not inside this hook.
	got := redactAttr([]string{"req"}, slog.String("authorization", "Bearer abc.def.ghi"))
	require.Equal(t, marker, got.Value.String())
}

// TestNew_AppliesRedaction_Logfmt routes a real logfmt logger
// through New() and asserts the wire-format output has the expected
// masking. Exercises the HandlerOptions.ReplaceAttr wiring done in
// newHandler against slog.NewTextHandler.
func TestNew_AppliesRedaction_Logfmt(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	opener := func(Opts) (io.Writer, io.Closer, string, error) {
		return &buf, noopCloser{}, "", nil
	}
	logger, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("http req",
		slog.String("authorization", "Bearer secret-token"),
		slog.String("x-scope-orgid", "tenant-prod"), //nolint:sloglint // HTTP header names are kebab-case per RFC 9110
		slog.String("user-agent", "a10r/test"))      //nolint:sloglint // HTTP header names are kebab-case per RFC 9110

	out := buf.String()
	require.Contains(t, out, "authorization=***")
	require.NotContains(t, out, "secret-token")
	require.Contains(t, out, "x-scope-orgid=tenant-prod",
		"routing identifier passes through unmasked")
	require.Contains(t, out, "user-agent=a10r/test")
}

// TestNew_AppliesRedaction_JSON exercises the JSON handler path
// (slog.NewJSONHandler) — separate Handler instance from the
// logfmt path, so masking has to be re-asserted against its
// output shape.
func TestNew_AppliesRedaction_JSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	opener := func(Opts) (io.Writer, io.Closer, string, error) {
		return &buf, noopCloser{}, "", nil
	}
	logger, closer, err := newWithOpener(Opts{Format: FormatJSON}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("http req",
		slog.String("authorization", "Bearer secret-token"),
		slog.String("x-scope-orgid", "tenant-prod")) //nolint:sloglint // HTTP header names are kebab-case per RFC 9110

	out := buf.String()
	require.Contains(t, out, `"authorization":"***"`)
	require.NotContains(t, out, "secret-token")
	require.Contains(t, out, `"x-scope-orgid":"tenant-prod"`)
}

// TestSecretKeys_ClosedSetUnchanged is the regression guard for ADR
// 0008's "closed set" claim: any addition or removal must be a
// deliberate ADR amendment, not an accidental commit. Listing the
// expected keys verbatim makes the diff loud when membership shifts.
func TestSecretKeys_ClosedSetUnchanged(t *testing.T) {
	t.Parallel()

	expected := []string{
		"access-token",
		"access_token",
		"api-key",
		"authorization",
		"bearer",
		"client-secret",
		"client_secret",
		"cookie",
		"credentials",
		"csrf",
		"password",
		"passwd",
		"private-key",
		"private_key",
		"proxy-authorization",
		"refresh-token",
		"refresh_token",
		"secret",
		"session",
		"sessionid",
		"set-cookie",
		"token",
		"x-api-key",
	}

	require.Len(t, secretKeys, len(expected),
		"secretKeys must remain a closed set per ADR 0008; new entries need an ADR amendment")
	for _, k := range expected {
		_, ok := secretKeys[k]
		require.Truef(t, ok, "expected key %q absent from secretKeys", k)
	}
}

// TestNew_StripsURLUserinfoFromMsg pins that credentials embedded in
// a record's Message (which slog.HandlerOptions.ReplaceAttr never
// sees) are scrubbed before the line lands in the file. Common leak
// shape: a backend client wraps its request URL into an error string
// and the caller logs the error verbatim. ReplaceAttr never sees
// record.Message, so without the msgRedactingHandler wrapper, the
// password lands in the log.
func TestNew_StripsURLUserinfoFromMsg(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opener := func(Opts) (io.Writer, io.Closer, string, error) {
		return &buf, noopCloser{}, "", nil
	}
	logger, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("connect failed: Get https://alice:hunter2@am.example.com/api/v2/alerts: i/o timeout")
	out := buf.String()
	require.NotContains(t, out, "hunter2",
		"password in URL userinfo must not land in the log file")
	require.NotContains(t, out, "alice",
		"userinfo username must also be stripped")
	require.Contains(t, out, "am.example.com",
		"host and the rest of the message must survive the strip")
}

// TestNew_StripsURLUserinfoFromAttrValues pins the parallel guard for
// string attr values: a `slog.String("url", "https://user:pass@host")`
// must also be stripped, since the attr key ("url") isn't a secret
// key on its own but the value carries credentials.
func TestNew_StripsURLUserinfoFromAttrValues(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opener := func(Opts) (io.Writer, io.Closer, string, error) {
		return &buf, noopCloser{}, "", nil
	}
	logger, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("backend probe",
		slog.String("url", "https://bob:topsecret@am.example.com/-/ready"))
	out := buf.String()
	require.NotContains(t, out, "topsecret")
	require.NotContains(t, out, "bob:")
	require.Contains(t, out, "am.example.com/-/ready")
}

// logValuerURL implements slog.LogValuer and resolves to a string
// value carrying URL userinfo. Mirrors the real-world shape where a
// backend client wraps a *url.URL-ish type as a slog.LogValuer so the
// caller can `slog.Any("conn", c)` without leaking the password in
// the underlying String() — yet the LogValue() path itself stringifies
// to the credential-bearing URL.
type logValuerURL struct{ raw string }

func (l logValuerURL) LogValue() slog.Value { return slog.StringValue(l.raw) }

// TestRedactAttr_StripsURLUserinfoFromLogValuer pins the KindLogValuer
// branch directly against redactAttr. slog's TextHandler/JSONHandler
// pre-resolve LogValuer before invoking ReplaceAttr (so the integration
// path through New() ends up exercising the KindString branch), but
// downstream handlers or test code calling redactAttr with an
// unresolved LogValuer must also strip the embedded credential.
func TestRedactAttr_StripsURLUserinfoFromLogValuer(t *testing.T) {
	t.Parallel()
	conn := logValuerURL{raw: "https://carol:swordfish@am.example.com/api/v2/alerts"}
	a := slog.Any("conn", conn)
	require.Equalf(t, slog.KindLogValuer, a.Value.Kind(),
		"sanity: raw slog.Any of a LogValuer surfaces as KindLogValuer")
	got := redactAttr(nil, a)
	require.NotContains(t, got.Value.String(), "swordfish",
		"password from LogValuer-resolved string must be stripped")
	require.NotContains(t, got.Value.String(), "carol:",
		"userinfo username from LogValuer must also be stripped")
	require.Contains(t, got.Value.String(), "am.example.com/api/v2/alerts",
		"host and path survive the strip")
}

// TestNew_StripsURLUserinfoFromLogValuer is the integration twin:
// even though slog's text handler resolves LogValuer before
// ReplaceAttr, this test pins the end-to-end guarantee that a
// LogValuer-wrapped credential never lands in the log file.
func TestNew_StripsURLUserinfoFromLogValuer(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opener := func(Opts) (io.Writer, io.Closer, string, error) {
		return &buf, noopCloser{}, "", nil
	}
	logger, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	conn := logValuerURL{raw: "https://carol:swordfish@am.example.com/api/v2/alerts"}
	logger.Info("dial", slog.Any("conn", conn))
	out := buf.String()
	require.NotContains(t, out, "swordfish",
		"password from LogValuer-resolved string must not land in the log")
	require.NotContains(t, out, "carol:",
		"userinfo username from LogValuer must also be stripped")
	require.Contains(t, out, "am.example.com/api/v2/alerts",
		"host and path survive the strip")
}

// stringerURL implements fmt.Stringer. A common shape: net/url.URL has
// String() that includes userinfo when the URL was constructed with
// user:pass — passing it via slog.Any wraps the value as
// slog.KindAny, not slog.KindString, so the attr-value scanner has to
// detect Stringer-ness explicitly.
type stringerURL struct{ raw string }

func (s stringerURL) String() string { return s.raw }

// TestNew_StripsURLUserinfoFromStringerAttr pins that values passed
// via slog.Any whose underlying type implements fmt.Stringer are also
// scanned. The handler will eventually call String() at render time;
// the scanner has to do the same and act on the result.
func TestNew_StripsURLUserinfoFromStringerAttr(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opener := func(Opts) (io.Writer, io.Closer, string, error) {
		return &buf, noopCloser{}, "", nil
	}
	logger, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	dest := stringerURL{raw: "redis://dave:p@ssw0rd@cache.example.com:6379/0"}
	logger.Info("dial", slog.Any("dest", dest))
	out := buf.String()
	require.NotContains(t, out, "p@ssw0rd",
		"password from Stringer must not land in the log")
	require.NotContains(t, out, "dave:",
		"Stringer userinfo username must also be stripped")
	require.Contains(t, out, "cache.example.com:6379/0",
		"host:port and path survive the strip")
}

// nonStringer carries no String() method — the security boundary for
// the KindAny branch is Stringer, and the scanner must NOT introspect
// random struct shapes. Sanity guard that an opaque value flows
// through without panic and without spurious redaction.
type nonStringer struct {
	Field string
}

// TestNew_PassesThroughNonStringer_AsAny is the regression guard for
// the Stringer boundary: a struct without String() must flow through
// the redactAttr KindAny branch untouched. Catches a future change
// that tries to scan via reflection — which would both break the
// security contract and risk panics on unexported fields.
func TestNew_PassesThroughNonStringer_AsAny(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	opener := func(Opts) (io.Writer, io.Closer, string, error) {
		return &buf, noopCloser{}, "", nil
	}
	logger, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("opaque", slog.Any("blob", nonStringer{Field: "value"}))
	out := buf.String()
	require.Contains(t, out, "blob=", "attr key still rendered")
	require.NotEmpty(t, out, "no panic, line emitted")
}
