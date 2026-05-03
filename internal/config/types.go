// SPDX-License-Identifier: Apache-2.0

// Package config defines the in-memory shape of `a10r.yaml` and the
// constants that anchor the user-facing defaults. Loading,
// env-variable interpolation, and CLI/env precedence resolution live
// in sibling files; this file is types only and contains no I/O.
//
// The schema mirrors Prometheus's `remote_write` block per
// docs/design/prometheus-remote-write-parity.md so a user can paste
// a `remote_write` entry under `backends:`, adjust the URL path, and
// be done. Field names and YAML tags are stable contracts: changing
// them is a schema break and requires a migration story.
package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// User-facing defaults pinned by the design docs. Each constant is
// the exact value referenced in a Resolved entry of open-questions.md
// so a `git grep` from either side surfaces the link.
const (
	// DefaultPollInterval matches I3 — 1 min, configurable per backend
	// and globally via defaults.poll_interval.
	DefaultPollInterval = 1 * time.Minute

	// DefaultThemeName matches M1 — catppuccin-mocha is the bundled
	// default; users override with theme.name in `a10r.yaml` or the
	// --theme CLI flag.
	DefaultThemeName = "catppuccin-mocha"

	// DefaultRemoteTimeout matches Prometheus's `remote_timeout`
	// default (30s). Picked large enough for a slow backend with
	// thousands of alerts but small enough that the polling loop does
	// not stall on a hung request — the C1 backoff cap is
	// poll_interval × 6, so a 30 s request timeout fits inside the
	// smallest sensible poll_interval (1 m default).
	DefaultRemoteTimeout = 30 * time.Second

	// DefaultAuthorizationType is the value Authorization.Type
	// resolves to when the user leaves it blank. Matches Prometheus
	// (HTTPClientConfig.Authorization.Type defaults to "Bearer").
	DefaultAuthorizationType = "Bearer"
)

// Config is the top-level shape of a10r.yaml. The Keys section is
// reserved for post-v0.1 user-defined keybinding overrides (J2) and
// is intentionally empty so the schema slot is locked in.
type Config struct {
	Backends []Backend `yaml:"backends,omitempty"`
	Defaults Defaults  `yaml:"defaults,omitempty"`
	Theme    Theme     `yaml:"theme,omitempty"`
	Log      Log       `yaml:"log,omitempty"`
	Keys     Keys      `yaml:"keys,omitempty"`
}

// Backend describes one Alertmanager (or Mimir) endpoint a10r polls.
// Field names match Prometheus's `remote_write` shape so a user can
// paste a remote_write entry under `backends:`, adjust the URL path,
// and be done.
//
// Auth blocks (BasicAuth, Authorization, BearerToken) are peers, of
// which at most one may be set per Backend — same rule as
// Prometheus's validateAuthConfigs.
//
// TenantHeader + Tenant are a10r-specific sugar for one entry of
// Headers. The factory layer materialises them into the same Headers
// map at construction time. Setting both Tenant and a colliding
// Headers entry is a loader error.
type Backend struct {
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
	Prefix string `yaml:"prefix,omitempty"`

	TenantHeader string `yaml:"tenant_header,omitempty"`
	Tenant       string `yaml:"tenant,omitempty"`

	BasicAuth     *BasicAuth        `yaml:"basic_auth,omitempty"`
	Authorization *Authorization    `yaml:"authorization,omitempty"`
	BearerToken   string            `yaml:"bearer_token,omitempty"`
	Headers       map[string]string `yaml:"headers,omitempty"`
	TLSConfig     *TLSConfig        `yaml:"tls_config,omitempty"`

	ProxyURL             string `yaml:"proxy_url,omitempty"`
	NoProxy              string `yaml:"no_proxy,omitempty"`
	ProxyFromEnvironment bool   `yaml:"proxy_from_environment,omitempty"`

	RemoteTimeout time.Duration `yaml:"remote_timeout,omitempty"`

	Capabilities Capabilities  `yaml:"capabilities,omitempty"`
	ReadOnly     bool          `yaml:"read_only,omitempty"`
	PollInterval time.Duration `yaml:"poll_interval,omitempty"`
}

// Validate runs the schema-level validation that struct tags alone
// cannot express — the "at most one auth block" rule, header
// reservation, proxy exclusivity, tenant_header / headers collision
// detection, and TLS subset checks. Called by the loader after a
// strict-mode YAML decode so misconfiguration surfaces at parse time
// rather than on the first poll.
//
// Validate also fills documented defaults — Authorization.Type
// resolves to "Bearer" when omitted — so downstream code observes
// one canonical shape.
//
// Implementing this as a method (rather than yaml.UnmarshalYAML) is
// deliberate: yaml.v3's KnownFields strict mode is a property of the
// decoder, not the node, and a custom UnmarshalYAML that calls
// node.Decode silently strips that contract. Validation runs over
// the already-decoded struct instead.
func (b *Backend) Validate() error {
	if err := b.validateAuthExclusive(); err != nil {
		return err
	}
	if err := b.validateAuthBlocks(); err != nil {
		return err
	}
	if err := validateHeaders(b.Headers); err != nil {
		return err
	}
	if err := b.validateTenantSugar(); err != nil {
		return err
	}
	if err := b.validateProxy(); err != nil {
		return err
	}
	if b.TLSConfig != nil {
		if err := b.TLSConfig.Validate(); err != nil {
			return fmt.Errorf("backend %q: %w", b.Name, err)
		}
	}
	return nil
}

// Validate walks every backend in the config. The first error wins —
// a single misconfigured backend halts startup so the user sees one
// problem at a time rather than a wall.
func (c *Config) Validate() error {
	for i := range c.Backends {
		if err := c.Backends[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) validateAuthExclusive() error {
	var configured []string
	if b.BasicAuth != nil {
		configured = append(configured, "basic_auth")
	}
	if b.Authorization != nil {
		configured = append(configured, "authorization")
	}
	if b.BearerToken != "" {
		configured = append(configured, "bearer_token")
	}
	if len(configured) > 1 {
		return fmt.Errorf("backend %q: at most one of basic_auth, authorization, bearer_token may be configured (got %v)", b.Name, configured)
	}
	return nil
}

func (b *Backend) validateAuthBlocks() error {
	if b.BasicAuth != nil {
		if b.BasicAuth.Username == "" || b.BasicAuth.Password == "" {
			return fmt.Errorf("backend %q: basic_auth requires both username and password", b.Name)
		}
	}
	if b.Authorization != nil {
		if b.Authorization.Credentials == "" {
			return fmt.Errorf("backend %q: authorization requires credentials", b.Name)
		}
		if b.Authorization.Type == "" {
			b.Authorization.Type = DefaultAuthorizationType
		}
	}
	return nil
}

func (b *Backend) validateTenantSugar() error {
	if (b.TenantHeader == "") != (b.Tenant == "") {
		return fmt.Errorf("backend %q: tenant_header and tenant must be set together", b.Name)
	}
	if b.TenantHeader == "" {
		return nil
	}
	if strings.EqualFold(b.TenantHeader, "Authorization") {
		return fmt.Errorf("backend %q: tenant_header may not be Authorization — use basic_auth, authorization, or bearer_token", b.Name)
	}
	for k := range b.Headers {
		if strings.EqualFold(k, b.TenantHeader) {
			return fmt.Errorf("backend %q: tenant_header %q collides with headers[%q] — set the value in one place", b.Name, b.TenantHeader, k)
		}
	}
	return nil
}

func (b *Backend) validateProxy() error {
	if b.ProxyFromEnvironment && (b.ProxyURL != "" || b.NoProxy != "") {
		return fmt.Errorf("backend %q: proxy_from_environment is exclusive with proxy_url and no_proxy", b.Name)
	}
	if b.ProxyURL == "" && b.NoProxy != "" {
		return fmt.Errorf("backend %q: no_proxy requires proxy_url", b.Name)
	}
	return nil
}

// reservedHeaders enumerates wire-level headers a user may not set
// via the `headers:` map. Mirrors Prometheus's reservedHeaders list:
// Authorization rides through the auth blocks, the rest are managed
// by a10r's transport layer.
var reservedHeaders = map[string]string{
	"authorization":    "use basic_auth, authorization, or bearer_token",
	"host":             "set the URL host instead",
	"content-type":     "set automatically by a10r",
	"content-length":   "set automatically by a10r",
	"content-encoding": "set automatically by a10r",
}

func validateHeaders(h map[string]string) error {
	for k := range h {
		if reason, ok := reservedHeaders[strings.ToLower(k)]; ok {
			return fmt.Errorf("headers[%q] is reserved (%s)", k, reason)
		}
	}
	return nil
}

// Capabilities are the explicit opt-in flags per audit §5.1 — nothing
// auto-enabled. v0.1 does not implement the underlying endpoints
// (Mimir config editor is deferred per A1) but the flags must still
// gate menu visibility once the action registry lands.
type Capabilities struct {
	ConfigAPI   bool `yaml:"config_api,omitempty"`
	TenantAdmin bool `yaml:"tenant_admin,omitempty"`
	Ring        bool `yaml:"ring,omitempty"`
}

// BasicAuth carries HTTP Basic credentials. Both fields are
// env-interpolatable (loader resolves `${VAR}` patterns per F1).
type BasicAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Authorization carries a generic `Authorization: <type> <credentials>`
// header. Type defaults to `Bearer` when omitted, matching
// Prometheus's HTTPClientConfig.Authorization. Credentials is
// env-interpolatable.
type Authorization struct {
	Type        string `yaml:"type,omitempty"`
	Credentials string `yaml:"credentials,omitempty"`
}

// TLSConfig configures TLS for the backend's HTTP transport. v0.1
// supports inline-only fields; the file-based and secret-manager
// variants (`*_file`, `*_ref`) are deferred per F1. `cert:` and
// `key:` are accepted by the schema but rejected by the validator
// until the mTLS work in F2 lands — the slot is reserved so a future
// addition is non-breaking.
type TLSConfig struct {
	CA                 string `yaml:"ca,omitempty"`
	Cert               string `yaml:"cert,omitempty"`
	Key                string `yaml:"key,omitempty"`
	ServerName         string `yaml:"server_name,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
	MinVersion         string `yaml:"min_version,omitempty"`
	MaxVersion         string `yaml:"max_version,omitempty"`
}

// validTLSVersions is the wire-level set Prometheus accepts in
// `min_version` / `max_version`. Pinned as a sentinel slice so the
// error message can echo the accepted values.
var validTLSVersions = []string{"TLS10", "TLS11", "TLS12", "TLS13"}

// Validate enforces the inline-only TLS subset and version-string
// contract. Called from Backend.Validate.
func (t *TLSConfig) Validate() error {
	if t.Cert != "" || t.Key != "" {
		return errors.New("tls_config.cert and tls_config.key are reserved for the mTLS implementation (see open-questions F2)")
	}
	if err := validateTLSVersion("min_version", t.MinVersion); err != nil {
		return err
	}
	return validateTLSVersion("max_version", t.MaxVersion)
}

func validateTLSVersion(field, v string) error {
	if v == "" {
		return nil
	}
	if !slices.Contains(validTLSVersions, v) {
		return fmt.Errorf("tls_config.%s must be one of %v (got %q)", field, validTLSVersions, v)
	}
	return nil
}

// Defaults are the global fallbacks consulted when a backend leaves a
// field unset (PollInterval) or when the entire backend list is
// running with one shared knob (ReadOnly, LogFormat).
//
// LogFormat lives here rather than on Log because Log captures
// path/level (sink-side state) whereas the format is a
// presentation-layer choice the user expects to override via the
// `--log-format` CLI flag without rewriting the Log block. The
// resolver merges them.
type Defaults struct {
	PollInterval time.Duration `yaml:"poll_interval,omitempty"`
	ReadOnly     bool          `yaml:"read_only,omitempty"`
	LogFormat    string        `yaml:"log_format,omitempty"`
}

// Theme picks the bundled or user-supplied skin per M1. An empty Name
// means DefaultThemeName.
type Theme struct {
	Name string `yaml:"name,omitempty"`
}

// Log holds the file logger configuration. Format selection lives on
// Defaults (LogFormat) so the same constant survives a per-CLI
// override; this struct carries Path and Level only.
type Log struct {
	Path  string `yaml:"path,omitempty"`
	Level string `yaml:"level,omitempty"`
}

// Keys is reserved for user-defined keybinding overrides (J2,
// post-v0.1). The struct is exported empty so the YAML key is part
// of the schema contract from day one and adding fields later is a
// non-breaking change.
type Keys struct{}
