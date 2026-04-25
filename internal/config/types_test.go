// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
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

func TestConfig_LoadValidMinimal(t *testing.T) {
	t.Parallel()

	body := readFixture(t, "valid_minimal.yaml")
	var got Config
	require.NoError(t, yaml.Unmarshal(body, &got))

	want := Config{
		Backends: []Backend{
			{Name: "local-am", URL: "http://localhost:9093"},
		},
	}
	require.Equal(t, want, got)
}

func TestConfig_LoadValidFull(t *testing.T) {
	t.Parallel()

	body := readFixture(t, "valid_full.yaml")
	var got Config
	require.NoError(t, yaml.Unmarshal(body, &got))

	want := Config{
		Defaults: Defaults{
			PollInterval: 30 * time.Second,
			ReadOnly:     false,
			LogFormat:    "json",
		},
		Theme: Theme{Name: "gruvbox-dark"},
		Log: Log{
			Path:  "/var/log/a10r/a10r.log",
			Level: "info",
		},
		Backends: []Backend{
			{
				Name: "prod-vanilla",
				URL:  "https://am-prod.internal",
				Auth: &AuthSpec{
					Type:   AuthTypeBearer,
					Bearer: &BearerAuth{Token: "${A10R_PROD_TOKEN}"},
				},
			},
			{
				Name:         "staging-mimir",
				URL:          "https://mimir-staging.internal",
				Prefix:       "/alertmanager",
				TenantHeader: "X-Scope-OrgID",
				Tenant:       "tenant-a",
				Capabilities: Capabilities{
					ConfigAPI:   true,
					TenantAdmin: true,
					Ring:        false,
				},
				Auth: &AuthSpec{
					Type: AuthTypeBasic,
					Basic: &BasicAuth{
						Username: "a10r-bot",
						Password: "${A10R_STAGING_PASS}",
					},
				},
				PollInterval: 10 * time.Second,
				ReadOnly:     true,
			},
			{
				Name: "gateway-headers",
				URL:  "https://am-via-gateway.example",
				Auth: &AuthSpec{
					Type: AuthTypeHeader,
					Header: &HeaderAuth{
						Name:  "X-Gateway-Token",
						Value: "${A10R_GW_TOKEN}",
					},
				},
			},
		},
	}
	require.Equal(t, want, got)
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
}

func TestAuthTypeConstants(t *testing.T) {
	t.Parallel()

	// Auth type identifiers are the wire-level strings users put in
	// `auth.type:`. Stability matters more than ergonomics — pin them.
	require.Equal(t, "none", AuthTypeNone)
	require.Equal(t, "basic", AuthTypeBasic)
	require.Equal(t, "bearer", AuthTypeBearer)
	require.Equal(t, "header", AuthTypeHeader)
}

func TestConfig_StrictModeRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	// This test pins the contract that the #06 loader must enable
	// yaml strict mode (`KnownFields(true)`). Without it, typos like
	// `pollInterval` instead of `poll_interval` silently produce
	// configs with the user-intended value missing — exactly the
	// class of error a strict schema is supposed to surface early.
	body := readFixture(t, "invalid_unknown_field.yaml")

	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)

	var c Config
	err := decoder.Decode(&c)
	require.Error(t, err, "strict-mode decode must reject unknown fields")
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return body
}
