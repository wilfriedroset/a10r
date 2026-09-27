// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"strings"
	"time"
	"unicode"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// sanitize replaces every control rune with one space. A remote
// Alertmanager serves any byte it likes, and a10r paints the result
// into a terminal, where an escape sequence repaints the frame, moves
// the cursor or sets the window title. The edge is the last place
// that still knows the string came off the wire. Tab and newline go
// too, because a table cell is one line by contract.
//
// The substitution is lossy on purpose: a cell renders one line, so a
// preserved escape has nothing to render into.
// A matcher built from a mangled label value therefore matches no
// alert, so a silence over such an alert is created and does nothing.
// That trade buys a terminal a remote backend cannot drive.
func sanitize(s string) string {
	if !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// sanitizeAll aliases in when nothing in it needs a substitution.
func sanitizeAll(in []string) []string {
	clean := true
	for _, s := range in {
		if strings.ContainsFunc(s, unicode.IsControl) {
			clean = false
			break
		}
	}
	if clean {
		return in
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = sanitize(s)
	}
	return out
}

// sanitizeMap aliases m when nothing in it needs a substitution,
// which is every poll of every healthy backend.
func sanitizeMap(m map[string]string) map[string]string {
	clean := true
	for k, v := range m {
		if strings.ContainsFunc(k, unicode.IsControl) || strings.ContainsFunc(v, unicode.IsControl) {
			clean = false
			break
		}
	}
	if clean {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[sanitize(k)] = sanitize(v)
	}
	return out
}

// toAlert and its siblings are the wire-to-domain converters used by
// every read-path Client method. Kept as plain functions (not methods)
// so they're trivially unit-testable and the dependency direction is
// one-way: domain types do not know wire types exist.
func toAlert(w wireAlert) backend.Alert {
	a := backend.Alert{
		Fingerprint:  w.Fingerprint,
		Labels:       sanitizeMap(w.Labels),
		Annotations:  sanitizeMap(w.Annotations),
		StartsAt:     w.StartsAt,
		EndsAt:       w.EndsAt,
		GeneratorURL: sanitize(w.GeneratorURL),
		State:        backend.AlertState(w.Status.State),
		// SilencedBy keys into the silence map the alert page builds
		// from toSilence, whose IDs are raw. Mangling one side alone
		// would turn every suppression row into the unknown marker.
		SilencedBy:  w.Status.SilencedBy,
		InhibitedBy: sanitizeAll(w.Status.InhibitedBy),
		MutedBy:     sanitizeAll(w.Status.MutedBy),
	}
	if len(w.Receivers) > 0 {
		a.Receivers = make([]string, 0, len(w.Receivers))
		for _, r := range w.Receivers {
			a.Receivers = append(a.Receivers, sanitize(r.Name))
		}
	}
	return a
}

func toSilence(w wireSilence) backend.Silence {
	s := backend.Silence{
		ID:        w.ID,
		StartsAt:  w.StartsAt,
		EndsAt:    w.EndsAt,
		CreatedBy: sanitize(w.CreatedBy),
		Comment:   sanitize(w.Comment),
		State:     backend.SilenceState(w.Status.State),
		UpdatedAt: w.UpdatedAt,
	}
	if len(w.Matchers) > 0 {
		s.Matchers = make([]backend.Matcher, 0, len(w.Matchers))
		for _, wm := range w.Matchers {
			s.Matchers = append(s.Matchers, toMatcher(wm))
		}
	}
	return s
}

// toMatcher resolves the IsEqual semantics: the wire field is
// optional and defaults to true (the positive form `=` / `=~`).
// nil → true; explicit false → false.
func toMatcher(w wireMatcher) backend.Matcher {
	isEqual := true
	if w.IsEqual != nil {
		isEqual = *w.IsEqual
	}
	return backend.Matcher{
		Name:    sanitize(w.Name),
		Value:   sanitize(w.Value),
		IsRegex: w.IsRegex,
		IsEqual: isEqual,
	}
}

func toReceiver(w wireReceiver) backend.Receiver {
	return backend.Receiver{Name: sanitize(w.Name)}
}

// toWireMatcher is the outbound conversion (domain → wire) used by
// CreateSilence/UpdateSilence. IsEqual is always emitted explicitly
// (pointer-to-bool, never nil) so the server sees the user's intent
// rather than relying on a server-side default.
func toWireMatcher(m backend.Matcher) wireMatcher {
	isEqual := m.IsEqual
	return wireMatcher{
		Name:    m.Name,
		Value:   m.Value,
		IsRegex: m.IsRegex,
		IsEqual: &isEqual,
	}
}

func toWireMatchers(in []backend.Matcher) []wireMatcher {
	if len(in) == 0 {
		return nil
	}
	out := make([]wireMatcher, 0, len(in))
	for _, m := range in {
		out = append(out, toWireMatcher(m))
	}
	return out
}

// toStatus converts /api/v2/status. Uptime is computed at decode
// time as `time.Since(wire.uptime)` — the wire format is the start
// timestamp, but the backend.Status type carries a duration so the
// renderer can format it directly.
func toStatus(w wireStatus, now func() time.Time) backend.Status {
	if now == nil {
		now = time.Now
	}
	return backend.Status{
		Cluster: backend.ClusterStatus{
			Status: sanitize(w.Cluster.Status),
			Peers:  toPeers(w.Cluster.Peers),
		},
		Version: backend.VersionInfo{
			Version:   sanitize(w.VersionInfo.Version),
			Revision:  sanitize(w.VersionInfo.Revision),
			Branch:    sanitize(w.VersionInfo.Branch),
			BuildUser: sanitize(w.VersionInfo.BuildUser),
			BuildDate: sanitize(w.VersionInfo.BuildDate),
			GoVersion: sanitize(w.VersionInfo.GoVersion),
		},
		// Config is the backend's whole alertmanager.yml and the page
		// splits it on "\n". A substitution there would flatten the
		// document into one unreadable line.
		Config: w.Config.Original,
		Uptime: now().Sub(w.Uptime),
	}
}

func toPeers(in []wireClusterPeer) []backend.ClusterPeer {
	if len(in) == 0 {
		return nil
	}
	out := make([]backend.ClusterPeer, 0, len(in))
	for _, p := range in {
		out = append(out, backend.ClusterPeer{Name: sanitize(p.Name), Address: sanitize(p.Address)})
	}
	return out
}
