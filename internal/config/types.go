// SPDX-License-Identifier: Apache-2.0

// Package config defines the in-memory shape of `a10r.yaml` and the
// constants that anchor the user-facing defaults. Loading,
// env-variable interpolation, and CLI/env precedence resolution live
// in sibling files added in follow-up commits; this file is types
// only and contains no I/O.
//
// The schema mirrors open-question B1 (single-file layout) and the
// per-backend sketch in docs/design/backend-api-audit.md §5.1. Field
// names and YAML tags are stable contracts: changing them is a
// schema break and requires a migration story.
package config

import "time"

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
)

// Auth type identifiers used in AuthSpec.Type. Pinned as constants so
// the loader and the validator can switch on them without string
// drift across packages.
const (
	AuthTypeNone   = "none"
	AuthTypeBasic  = "basic"
	AuthTypeBearer = "bearer"
	AuthTypeHeader = "header"
)

// Config is the top-level shape of a10r.yaml — one file with five
// sections per B1. The Keys section is reserved for post-v0.1
// user-defined keybinding overrides (J2) and is intentionally empty
// so the schema slot is locked in.
type Config struct {
	Backends []Backend `yaml:"backends,omitempty"`
	Defaults Defaults  `yaml:"defaults,omitempty"`
	Theme    Theme     `yaml:"theme,omitempty"`
	Log      Log       `yaml:"log,omitempty"`
	Keys     Keys      `yaml:"keys,omitempty"`
}

// Backend describes a single (URL + optional tenant header + tenant
// value) tuple. Each entry produces one client per audit §5.1's "one
// constructor, config-driven" rule. Per-backend ReadOnly and
// PollInterval override Defaults; an empty / zero value means
// "inherit from defaults".
type Backend struct {
	Name         string       `yaml:"name"`
	URL          string       `yaml:"url"`
	Prefix       string       `yaml:"prefix,omitempty"`
	TenantHeader string       `yaml:"tenant_header,omitempty"`
	Tenant       string       `yaml:"tenant,omitempty"`
	Capabilities Capabilities `yaml:"capabilities,omitempty"`
	// Auth is a pointer so the loader and resolver can distinguish
	// "user did not configure auth" (nil) from "user explicitly chose
	// no auth" (&AuthSpec{Type: AuthTypeNone}).
	Auth         *AuthSpec     `yaml:"auth,omitempty"`
	ReadOnly     bool          `yaml:"read_only,omitempty"`
	PollInterval time.Duration `yaml:"poll_interval,omitempty"`
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

// AuthSpec selects the per-backend auth method per F1. v0.1 covers
// none / basic / bearer / header; mTLS (F2) and SigV4 (F3) are
// deferred — the AuthSpec shape preserves the slot but the loader
// will reject those Type values until the backing implementation
// lands.
//
// AuthSpec is the natural extension point for type-driven validation
// (e.g. require Basic when Type==basic). Implementing yaml.Unmarshaler
// here in #06 will keep the rule next to the schema rather than
// scattered across the loader.
type AuthSpec struct {
	Type   string      `yaml:"type,omitempty"`
	Basic  *BasicAuth  `yaml:"basic,omitempty"`
	Bearer *BearerAuth `yaml:"bearer,omitempty"`
	Header *HeaderAuth `yaml:"header,omitempty"`
}

// BasicAuth carries HTTP Basic credentials. Both fields are env-
// interpolatable (loader resolves `${VAR}` patterns per F1).
type BasicAuth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// BearerAuth carries an HTTP Bearer token. Token is env-interpolatable.
// Modelled as a struct (rather than a bare string under AuthSpec) so
// future fields (token_file when mTLS lands, refresh hooks, …) can
// land additively without breaking the YAML schema.
type BearerAuth struct {
	Token string `yaml:"token"`
}

// HeaderAuth injects a single arbitrary header (e.g.
// `X-Some-Gateway-Token: foo`). For multiple custom headers, repeat
// the auth at the proxy or extend HeaderAuth post-v0.1.
type HeaderAuth struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// Defaults are the global fallbacks consulted when a backend leaves a
// field unset (PollInterval) or when the entire backend list is
// running with one shared knob (ReadOnly, LogFormat).
//
// LogFormat lives here rather than on Log because Log captures
// path/level (sink-side state) whereas the format is a
// presentation-layer choice the user expects to override via the
// `--log-format` CLI flag without rewriting the Log block. The #07
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
