// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfig_ZeroValueRoundTrips(t *testing.T) {
	t.Parallel()

	// A zero-value Config must marshal to a document that unmarshals
	// back to the same zero value — proves every `omitempty` is
	// correct and no field carries a hidden default that the marshal
	// step injects.
	var zero Config
	bytesOut, err := yaml.Marshal(zero)
	require.NoError(t, err)

	var round Config
	require.NoError(t, yaml.Unmarshal(bytesOut, &round))
	require.Equal(t, zero, round)
}

func TestConfig_LoadValidFull(t *testing.T) {
	t.Parallel()

	body := readFixture(t, "valid_full.yaml")
	var got Config
	require.NoError(t, yaml.Unmarshal(body, &got))

	require.Len(t, got.Backends, 3)

	// First backend: bearer_token shorthand.
	require.Equal(t, "prod-vanilla", got.Backends[0].Name)
	require.Equal(t, "${A10R_PROD_TOKEN}", got.Backends[0].BearerToken)
	require.Nil(t, got.Backends[0].BasicAuth)
	require.Nil(t, got.Backends[0].Authorization)

	// Second backend: basic_auth + tenant + tls + proxy.
	be := got.Backends[1]
	require.Equal(t, "staging-mimir", be.Name)
	require.Equal(t, "X-Scope-OrgID", be.TenantHeader)
	require.Equal(t, "tenant-a", be.Tenant)
	require.NotNil(t, be.BasicAuth)
	require.Equal(t, "a10r-bot", be.BasicAuth.Username)
	require.Equal(t, "${A10R_STAGING_PASS}", be.BasicAuth.Password)
	require.NotNil(t, be.TLSConfig)
	require.Contains(t, be.TLSConfig.CA, "BEGIN CERTIFICATE")
	require.Equal(t, "mimir-staging.internal", be.TLSConfig.ServerName)
	require.Equal(t, "TLS12", be.TLSConfig.MinVersion)
	require.Equal(t, "http://proxy.internal:3128", be.ProxyURL)
	require.Equal(t, "127.0.0.1,localhost,.svc.cluster.local", be.NoProxy)
	require.Equal(t, 15*time.Second, be.RemoteTimeout)
	require.Equal(t, 10*time.Second, be.PollInterval)
	require.True(t, be.ReadOnly)
	require.True(t, be.Capabilities.ConfigAPI)
	require.True(t, be.Capabilities.TenantAdmin)
	require.False(t, be.Capabilities.Ring)

	// Third backend: arbitrary headers map.
	require.Equal(t, "gateway-headers", got.Backends[2].Name)
	require.Equal(t, "${A10R_GW_TOKEN}", got.Backends[2].Headers["X-Gateway-Token"])
	require.Equal(t, "a10r", got.Backends[2].Headers["X-Trace-Id"])
}

func TestConfig_RoundTripPreservesEverything(t *testing.T) {
	t.Parallel()

	// Take the full fixture, unmarshal, marshal, unmarshal again, and
	// require structural equality. Catches a class of yaml-tag bugs
	// where a field deserialises but doesn't re-emit cleanly (e.g. a
	// missing tag falls back to lowercased field name).
	body := readFixture(t, "valid_full.yaml")
	var first Config
	require.NoError(t, yaml.Unmarshal(body, &first))

	out, err := yaml.Marshal(first)
	require.NoError(t, err)

	var second Config
	require.NoError(t, yaml.Unmarshal(out, &second))
	require.Equal(t, first, second)
}

func TestDefaultsAreThePinnedConstants(t *testing.T) {
	t.Parallel()

	// Pin the constants to the values referenced from the design docs;
	// changing them is a deliberate schema decision and must update
	// open-questions.md alongside.
	require.Equal(t, time.Minute, DefaultPollInterval, "I3 fixes the default poll interval at 1m")
	require.Equal(t, "catppuccin-mocha", DefaultThemeName, "M1 fixes the default theme name")
	require.Equal(t, 30*time.Second, DefaultRemoteTimeout,
		"DefaultRemoteTimeout matches Prometheus's remote_timeout default")
	require.Equal(t, "Bearer", DefaultAuthorizationType,
		"DefaultAuthorizationType matches Prometheus's HTTPClientConfig.Authorization default")
	require.Equal(t, 4, DefaultBulkConcurrency,
		"DefaultBulkConcurrency is the per-tenant worker-pool size when defaults.bulk_concurrency is unset")
}

func TestDefaults_BulkConcurrencyDefaultsTo4WhenOmitted(t *testing.T) {
	t.Parallel()

	// A YAML document with no bulk_concurrency key must leave the
	// field at its zero value, and BulkConcurrencyOrDefault must
	// resolve that to DefaultBulkConcurrency.
	body := []byte("poll_interval: 30s\n")
	var d Defaults
	require.NoError(t, yaml.Unmarshal(body, &d))
	require.Equal(t, 0, d.BulkConcurrency, "absent key must leave the field at zero")
	require.Equal(t, DefaultBulkConcurrency, d.BulkConcurrencyOrDefault())
}

func TestDefaults_BulkConcurrencyExplicitValuePreserved(t *testing.T) {
	t.Parallel()

	cases := []int{1, 2, 4, 8, 32}
	for _, n := range cases {
		d := Defaults{BulkConcurrency: n}
		require.Equal(t, n, d.BulkConcurrencyOrDefault(), "explicit %d must round-trip through the helper", n)
	}
}

func TestDefaults_BulkConcurrencyRejectsNegative(t *testing.T) {
	t.Parallel()

	d := Defaults{BulkConcurrency: -1}
	err := d.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "bulk_concurrency must be >= 0")

	// Config.Validate must surface the same error so a negative knob
	// halts startup rather than silently being clamped.
	c := Config{Defaults: Defaults{BulkConcurrency: -7}}
	err = c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "bulk_concurrency must be >= 0")
}

func TestTUI_DefaultIsTipsOff(t *testing.T) {
	t.Parallel()

	// The tui: block is OFF by default — a zero-value Config must
	// expose Tips=false and TipsInterval=0 (the wiring-layer fall
	// back substitutes footer.DefaultHintBarInterval). This is the
	// "no scouted features without explicit go" project rule made
	// load-bearing in the type definition: the user must explicitly
	// set `tui.tips: true` to opt in.
	var c Config
	require.False(t, c.TUI.Tips, "tui.tips must default to false")
	require.Zero(t, c.TUI.TipsInterval,
		"tui.tips_interval must default to zero (wiring fills in the package default)")
}

func TestTUI_RoundTrip(t *testing.T) {
	t.Parallel()

	body := []byte("tui:\n  tips: true\n  tips_interval: 12s\n")
	var c Config
	require.NoError(t, yaml.Unmarshal(body, &c))
	require.True(t, c.TUI.Tips, "explicit tui.tips: true must round-trip")
	require.Equal(t, 12*time.Second, c.TUI.TipsInterval,
		"tui.tips_interval must parse as a duration string")
}

func TestDefaults_BulkConcurrencyZeroPassesValidate(t *testing.T) {
	t.Parallel()

	// Zero is a legal "use the default" sentinel — Validate must accept
	// it so a brand-new config without the key parses cleanly.
	d := Defaults{BulkConcurrency: 0}
	require.NoError(t, d.Validate())
}

func TestBackend_AuthMethodsAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "basic_auth and bearer_token",
			yaml: `name: x
url: http://x
basic_auth:
  username: u
  password: p
bearer_token: tok
`,
		},
		{
			name: "basic_auth and authorization",
			yaml: `name: x
url: http://x
basic_auth:
  username: u
  password: p
authorization:
  credentials: tok
`,
		},
		{
			name: "authorization and bearer_token",
			yaml: `name: x
url: http://x
authorization:
  credentials: tok
bearer_token: tok2
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var be Backend
			require.NoError(t, yaml.Unmarshal([]byte(tc.yaml), &be))
			err := be.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), "at most one of basic_auth, authorization, bearer_token")
		})
	}
}

func TestBackend_AuthorizationDefaultsToBearer(t *testing.T) {
	t.Parallel()

	body := []byte(`name: x
url: http://x
authorization:
  credentials: tok
`)
	var be Backend
	require.NoError(t, yaml.Unmarshal(body, &be))
	require.NoError(t, be.Validate())
	require.NotNil(t, be.Authorization)
	require.Equal(t, "Bearer", be.Authorization.Type,
		"omitted authorization.type must default to Bearer per Prometheus")
}

func TestBackend_BasicAuthRequiresBothFields(t *testing.T) {
	t.Parallel()

	cases := []string{
		`name: x
url: http://x
basic_auth:
  username: u
`,
		`name: x
url: http://x
basic_auth:
  password: p
`,
	}
	for _, src := range cases {
		var be Backend
		require.NoError(t, yaml.Unmarshal([]byte(src), &be))
		err := be.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "basic_auth requires both username and password")
	}
}

func TestBackend_AuthorizationRequiresCredentials(t *testing.T) {
	t.Parallel()

	body := []byte(`name: x
url: http://x
authorization:
  type: Bearer
`)
	var be Backend
	require.NoError(t, yaml.Unmarshal(body, &be))
	err := be.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "authorization requires credentials")
}

func TestBackend_HeadersRejectsReservedKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		key    string
		reason string
	}{
		{name: "Authorization", key: "Authorization", reason: "use basic_auth"},
		{name: "lower-case authorization", key: "authorization", reason: "use basic_auth"},
		{name: "Host", key: "Host", reason: "URL host"},
		{name: "Content-Type", key: "Content-Type", reason: "automatically"},
		{name: "Content-Length", key: "Content-Length", reason: "automatically"},
		{name: "Content-Encoding", key: "Content-Encoding", reason: "automatically"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := []byte("name: x\nurl: http://x\nheaders:\n  " + tc.key + ": value\n")
			var be Backend
			require.NoError(t, yaml.Unmarshal(body, &be))
			err := be.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), "is reserved")
			require.Contains(t, err.Error(), tc.reason)
		})
	}
}

func TestBackend_TenantSugarRequiresBothFields(t *testing.T) {
	t.Parallel()

	cases := []string{
		"name: x\nurl: http://x\ntenant: foo\n",
		"name: x\nurl: http://x\ntenant_header: X-Scope-OrgID\n",
	}
	for _, src := range cases {
		var be Backend
		require.NoError(t, yaml.Unmarshal([]byte(src), &be))
		err := be.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "tenant_header and tenant must be set together")
	}
}

func TestBackend_TenantHeaderCollisionWithHeadersMap(t *testing.T) {
	t.Parallel()

	body := []byte(`name: x
url: http://x
tenant_header: X-Scope-OrgID
tenant: foo
headers:
  X-Scope-Orgid: bar
`)
	var be Backend
	require.NoError(t, yaml.Unmarshal(body, &be))
	err := be.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "collides")
}

func TestBackend_TenantHeaderRejectsAuthorization(t *testing.T) {
	t.Parallel()

	body := []byte(`name: x
url: http://x
tenant_header: Authorization
tenant: foo
`)
	var be Backend
	require.NoError(t, yaml.Unmarshal(body, &be))
	err := be.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "may not be Authorization")
}

func TestBackend_ProxyValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name: "no_proxy without proxy_url",
			yaml: `name: x
url: http://x
no_proxy: 127.0.0.1
`,
			wantSub: "no_proxy requires proxy_url",
		},
		{
			name: "proxy_from_environment with proxy_url",
			yaml: `name: x
url: http://x
proxy_from_environment: true
proxy_url: http://proxy
`,
			wantSub: "exclusive",
		},
		{
			name: "proxy_from_environment with no_proxy",
			yaml: `name: x
url: http://x
proxy_from_environment: true
no_proxy: 127.0.0.1
`,
			wantSub: "exclusive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var be Backend
			require.NoError(t, yaml.Unmarshal([]byte(tc.yaml), &be))
			err := be.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantSub)
		})
	}
}

func TestTLSConfig_VersionStringValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{name: "valid TLS12", yaml: "min_version: TLS12\n", wantErr: false},
		{name: "valid TLS10/TLS13", yaml: "min_version: TLS10\nmax_version: TLS13\n", wantErr: false},
		{name: "empty", yaml: "ca: pem\n", wantErr: false},
		{name: "wrong-case tls12", yaml: "min_version: tls12\n", wantErr: true},
		{name: "garbage", yaml: "max_version: latest\n", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var tls TLSConfig
			require.NoError(t, yaml.Unmarshal([]byte(tc.yaml), &tls))
			err := tls.Validate()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTLSConfig_CertKeyReservedForF2(t *testing.T) {
	t.Parallel()

	cases := []string{
		"cert: |\n  -----BEGIN CERT-----\n",
		"key: |\n  -----BEGIN KEY-----\n",
	}
	for _, src := range cases {
		var tls TLSConfig
		require.NoError(t, yaml.Unmarshal([]byte(src), &tls))
		err := tls.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "F2")
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return body
}
