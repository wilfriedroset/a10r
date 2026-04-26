// SPDX-License-Identifier: Apache-2.0

// Package backend defines the dual-target client surface for vanilla
// Prometheus Alertmanager v2 and Grafana Mimir. Implementations
// (vanilla in internal/backend/vanilla, Mimir in internal/backend/mimir,
// fan-out in internal/backend/multi) live alongside; this package
// holds only the types and the interface contract.
//
// The shape mirrors `docs/design/backend-api-audit.md` §5.1: one
// constructor for both backends (the Mimir wrapper composes vanilla
// with prefix and tenant header), capability-gated methods return
// ErrUnsupported on backends that do not enable them, and HTTP-level
// concerns (auth, redirects, timeouts) live behind a pluggable
// http.RoundTripper rather than leaking into this surface.
package backend

import "time"

// AlertState mirrors `status.state` from /api/v2/alerts.
type AlertState string

// Documented states per audit §1.3.
const (
	AlertStateActive      AlertState = "active"
	AlertStateSuppressed  AlertState = "suppressed"
	AlertStateUnprocessed AlertState = "unprocessed"
)

// Alert is the in-memory shape of one entry from /api/v2/alerts. The
// fields the v0.1 TUI consumes are first-class; the few audit-listed
// fields the TUI does not currently render (e.g. fingerprintExt) are
// dropped to avoid carrying dead state.
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

// SilenceState mirrors `status.state` from /api/v2/silences.
type SilenceState string

// Documented silence states per audit §1.3.
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
// `config.original` — the only way to inspect routes and inhibition
// rules per audit §1.3.
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
// which served the alerts list). v0.1 does not implement reads or
// writes against this surface — the Mimir wrapper returns
// ErrUnsupported for every method that touches it — but the type is
// locked in so the post-v0.1 editor can land additively.
//
// SemVer note: stub types in this block (MimirConfig, TenantConfig,
// Ring, RingInstance) carry no implementation in v0.1 and may grow
// fields before the post-v0.1 Mimir config editor ships. Treat them
// as unstable until the editor lands; downstream consumers that
// import these types should expect non-breaking field additions.
type MimirConfig struct {
	AlertmanagerConfig string
	TemplateFiles      map[string]string
}

// TenantConfig is one entry in the multi-tenant config listing
// (Mimir admin). v0.1 stub; see MimirConfig SemVer note.
type TenantConfig struct {
	Tenant string
	Config string
}

// Ring is the response shape of /multitenant_alertmanager/ring.
// v0.1 stub; see MimirConfig SemVer note.
type Ring struct {
	Instances []RingInstance
}

// RingInstance is one node in the Mimir hash ring. v0.1 stub; see
// MimirConfig SemVer note.
type RingInstance struct {
	ID     string
	Addr   string
	State  string
	Tokens []uint32
}
