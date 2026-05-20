// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// toAlert and its siblings are the wire-to-domain converters used by
// every read-path Client method. Kept as plain functions (not methods)
// so they're trivially unit-testable and the dependency direction is
// one-way: domain types do not know wire types exist.
func toAlert(w wireAlert) backend.Alert {
	a := backend.Alert{
		Fingerprint:  w.Fingerprint,
		Labels:       w.Labels,
		Annotations:  w.Annotations,
		StartsAt:     w.StartsAt,
		EndsAt:       w.EndsAt,
		GeneratorURL: w.GeneratorURL,
		State:        backend.AlertState(w.Status.State),
		SilencedBy:   w.Status.SilencedBy,
		InhibitedBy:  w.Status.InhibitedBy,
		MutedBy:      w.Status.MutedBy,
	}
	if len(w.Receivers) > 0 {
		a.Receivers = make([]string, 0, len(w.Receivers))
		for _, r := range w.Receivers {
			a.Receivers = append(a.Receivers, r.Name)
		}
	}
	return a
}

func toAlertGroup(w wireAlertGroup) backend.AlertGroup {
	g := backend.AlertGroup{Labels: w.Labels}
	if len(w.Alerts) > 0 {
		g.Alerts = make([]backend.Alert, 0, len(w.Alerts))
		for _, wa := range w.Alerts {
			g.Alerts = append(g.Alerts, toAlert(wa))
		}
	}
	return g
}

func toSilence(w wireSilence) backend.Silence {
	s := backend.Silence{
		ID:        w.ID,
		StartsAt:  w.StartsAt,
		EndsAt:    w.EndsAt,
		CreatedBy: w.CreatedBy,
		Comment:   w.Comment,
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
		Name:    w.Name,
		Value:   w.Value,
		IsRegex: w.IsRegex,
		IsEqual: isEqual,
	}
}

func toReceiver(w wireReceiver) backend.Receiver {
	return backend.Receiver{Name: w.Name}
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
			Status: w.Cluster.Status,
			Peers:  toPeers(w.Cluster.Peers),
		},
		Version: backend.VersionInfo{
			Version:   w.VersionInfo.Version,
			Revision:  w.VersionInfo.Revision,
			Branch:    w.VersionInfo.Branch,
			BuildUser: w.VersionInfo.BuildUser,
			BuildDate: w.VersionInfo.BuildDate,
			GoVersion: w.VersionInfo.GoVersion,
		},
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
		out = append(out, backend.ClusterPeer{Name: p.Name, Address: p.Address})
	}
	return out
}
