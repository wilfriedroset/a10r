// SPDX-License-Identifier: Apache-2.0

package pagetest

import (
	"maps"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// AlertOptions is the option-struct shape behind Alert. Every
// field has a sensible default so a zero-value Options still
// produces a renderable Alert — the field set covers what the
// per-page mkAlert helpers used to parameterise.
type AlertOptions struct {
	Name        string
	Severity    string
	State       backend.AlertState
	Now         time.Time
	Age         time.Duration
	Fingerprint string
	Labels      map[string]string
	Annotations map[string]string
	SilencedBy  []string
	InhibitedBy []string
	MutedBy     []string
	Receivers   []string
}

// Alert builds a synthetic backend.Alert from opts. Missing fields
// fall back to a stable, non-zero default so tests get deterministic
// output without re-stating the obvious. Name and Severity merge
// into the returned Labels map alongside any extra Labels the
// caller supplied — the caller-supplied entries win on conflict so
// per-test overrides remain straightforward.
func Alert(opts AlertOptions) backend.Alert {
	now := opts.Now
	if now.IsZero() {
		now = defaultNow
	}
	name := opts.Name
	if name == "" {
		name = "TestAlert"
	}
	severity := opts.Severity
	if severity == "" {
		severity = "warning"
	}
	state := opts.State
	if state == "" {
		state = backend.AlertStateActive
	}
	age := opts.Age
	if age == 0 {
		age = time.Minute
	}

	labels := map[string]string{
		"alertname": name,
		"severity":  severity,
	}
	maps.Copy(labels, opts.Labels)

	return backend.Alert{
		Labels:       labels,
		Annotations:  opts.Annotations,
		Fingerprint:  opts.Fingerprint,
		State:        state,
		StartsAt:     now.Add(-age),
		SilencedBy:   opts.SilencedBy,
		InhibitedBy:  opts.InhibitedBy,
		MutedBy:      opts.MutedBy,
		Receivers:    opts.Receivers,
		GeneratorURL: "",
	}
}

// SilenceOptions is the option-struct shape behind Silence. Both
// StartsIn and EndsIn are durations relative to Now — StartsIn is
// typically negative (silence already started) and EndsIn positive
// (silence ends N from now). Zero-Now uses the package default
// clock so tests don't have to wire fixedNow through.
type SilenceOptions struct {
	ID        string
	CreatedBy string
	State     backend.SilenceState
	Now       time.Time
	StartsIn  time.Duration
	EndsIn    time.Duration
	Comment   string
	Matchers  []backend.Matcher
	UpdatedAt time.Time
}

// Silence builds a synthetic backend.Silence from opts. The
// StartsIn / EndsIn naming surfaces the relative-to-Now semantic
// so tests express durations rather than absolute timestamps.
func Silence(opts SilenceOptions) backend.Silence {
	now := opts.Now
	if now.IsZero() {
		now = defaultNow
	}
	id := opts.ID
	if id == "" {
		id = "sil-default"
	}
	createdBy := opts.CreatedBy
	if createdBy == "" {
		createdBy = "tester"
	}
	state := opts.State
	if state == "" {
		state = backend.SilenceStateActive
	}
	startsIn := opts.StartsIn
	if startsIn == 0 {
		startsIn = -time.Hour
	}
	endsIn := opts.EndsIn
	if endsIn == 0 {
		endsIn = time.Hour
	}

	return backend.Silence{
		ID:        id,
		CreatedBy: createdBy,
		State:     state,
		StartsAt:  now.Add(startsIn),
		EndsAt:    now.Add(endsIn),
		Comment:   opts.Comment,
		Matchers:  opts.Matchers,
		UpdatedAt: opts.UpdatedAt,
	}
}

// GroupOptions is the option-struct shape behind Group. Labels
// double as the common-label set the group renders by; Alerts is
// the leaf list.
type GroupOptions struct {
	Labels map[string]string
	Alerts []backend.Alert
}

// Group builds a synthetic backend.AlertGroup from opts. Empty
// Labels falls back to a single-key map so the page's commonLabels
// path has something non-empty to project.
func Group(opts GroupOptions) backend.AlertGroup {
	labels := opts.Labels
	if len(labels) == 0 {
		labels = map[string]string{"team": "default"}
	}
	return backend.AlertGroup{
		Labels: labels,
		Alerts: opts.Alerts,
	}
}

// defaultNow is the deterministic clock baseline every fixture
// builder falls back to when callers leave Now unset. Picked to
// match the historical fixedNow value used across the per-page
// test files so migrated tests get bit-for-bit identical timestamps
// without restating the constant.
var defaultNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
