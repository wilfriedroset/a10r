// SPDX-License-Identifier: Apache-2.0

// Package backend defines the dual-target client surface for vanilla
// Prometheus Alertmanager v2 and Grafana Mimir. Implementations
// (vanilla in internal/backend/vanilla, Mimir in internal/backend/mimir,
// fan-out in internal/backend/multi) live alongside; this package
// holds only the types and the interface contract. See ADR 0028 for
// the one-constructor / config-driven shape and the rationale for
// keeping HTTP concerns behind a pluggable http.RoundTripper.
package backend

import "time"

// AlertState mirrors `status.state` from /api/v2/alerts (active,
// suppressed, unprocessed).
type AlertState string

const (
	AlertStateActive      AlertState = "active"
	AlertStateSuppressed  AlertState = "suppressed"
	AlertStateUnprocessed AlertState = "unprocessed"
)

// Alert is the in-memory shape of one entry from /api/v2/alerts. The
// fields the TUI consumes are first-class; fields the TUI does not
// render (e.g. fingerprintExt) are dropped to avoid carrying dead
// state.
//
// Labels and Annotations are maps. Implementations MUST NOT share
// the underlying map across goroutines — the polling loop replaces
// the snapshot wholesale, and the renderers read from a single
// goroutine, but a future "edit a label client-side then submit"
// flow would race. Callers that need to mutate must copy first.
type Alert struct {
	Labels       map[string]string
	Annotations  map[string]string
	Fingerprint  string
	StartsAt     time.Time
	EndsAt       time.Time
	GeneratorURL string
	State        AlertState
	SilencedBy   []string
	InhibitedBy  []string
	// MutedBy lists the names of mute_time_intervals the route
	// matched for this alert. Populated alongside SilencedBy /
	// InhibitedBy on /api/v2/alerts when status.state ==
	// "suppressed". The Alertmanager v2 OpenAPI schema marks the
	// field required, but a non-conforming proxy could omit it —
	// callers should treat empty as "no time-window mute" rather
	// than "field absent".
	MutedBy   []string
	Receivers []string
}

// AlertGroup is one node in /api/v2/alerts/groups output: a label
// set shared by every alert in Alerts.
type AlertGroup struct {
	Labels map[string]string
	Alerts []Alert
}

// SilenceState mirrors `status.state` from /api/v2/silences (active,
// pending, expired).
type SilenceState string

const (
	SilenceStateActive  SilenceState = "active"
	SilenceStatePending SilenceState = "pending"
	SilenceStateExpired SilenceState = "expired"
)

// Silence is the in-memory shape of one entry from /api/v2/silences.
type Silence struct {
	ID        string
	Matchers  []Matcher
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	Comment   string
	State     SilenceState
	UpdatedAt time.Time
}

// Matcher is a single Prometheus-style label matcher. IsRegex
// distinguishes `=` / `!=` from `=~` / `!~`; IsEqual distinguishes
// the positive (`=`, `=~`) from the negative (`!=`, `!~`) form.
type Matcher struct {
	Name    string
	Value   string
	IsRegex bool
	IsEqual bool
}

// Receiver is a route target name, returned by /api/v2/receivers.
type Receiver struct {
	Name string
}

// Status mirrors /api/v2/status. Config is the raw YAML from
// `config.original` — the only wire-exposed way to inspect routes
// and inhibition rules.
type Status struct {
	Cluster ClusterStatus
	Version VersionInfo
	Config  string
	Uptime  time.Duration
}

// ClusterStatus is the cluster sub-block of /api/v2/status.
type ClusterStatus struct {
	Status string
	Peers  []ClusterPeer
}

// ClusterPeer is one entry in the gossip ring.
type ClusterPeer struct {
	Name    string
	Address string
}

// VersionInfo is the buildinfo block of /api/v2/status.
type VersionInfo struct {
	Version   string
	Revision  string
	Branch    string
	BuildUser string
	BuildDate string
	GoVersion string
}

// MimirConfig is the payload of Mimir's /api/v1/alerts (the *config*
// endpoint — distinct from vanilla AM's now-removed /api/v1/alerts
// which served the alerts list). Every backend currently returns
// ErrUnsupported for the methods that touch it.
//
// Unstable: MimirConfig and its siblings (TenantConfig, Ring,
// RingInstance) carry no implementation, so downstream consumers
// should expect non-breaking field additions.
type MimirConfig struct {
	AlertmanagerConfig string
	TemplateFiles      map[string]string
}

// TenantConfig is one entry in the multi-tenant config listing
// (Mimir admin). See MimirConfig for stability notes.
type TenantConfig struct {
	Tenant string
	Config string
}

// Ring is the response shape of /multitenant_alertmanager/ring.
// See MimirConfig for stability notes.
type Ring struct {
	Instances []RingInstance
}

// RingInstance is one node in the Mimir hash ring. See MimirConfig
// for stability notes.
type RingInstance struct {
	ID     string
	Addr   string
	State  string
	Tokens []uint32
}
