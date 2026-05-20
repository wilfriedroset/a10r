// SPDX-License-Identifier: Apache-2.0

package backend

import "time"

// AlertFilter is the query-param shape of /api/v2/alerts. Pointer
// bools allow callers to distinguish "no filter on that param"
// (nil) from "filter to false explicitly". Filter is a list of
// Prometheus-style matcher strings (`alertname="High CPU"`, etc.);
// the client URL-encodes each entry as a separate `filter=` param
// because /api/v2/alerts treats repeated `filter=` as AND.
type AlertFilter struct {
	Active      *bool
	Silenced    *bool
	Inhibited   *bool
	Unprocessed *bool
	Filter      []string
	Receiver    string
}

// SilenceFilter mirrors AlertFilter for /api/v2/silences. Matchers
// here apply to silence MATCHERS, not alert labels — same wire
// format, different domain.
type SilenceFilter struct {
	Filter []string
}

// SilenceSpec is the create / update payload for /api/v2/silences.
// The interface uses one type for both verbs because Alertmanager
// distinguishes them by whether `id` is set on the wire — but the
// caller passes the id as a separate argument, keeping the spec
// reusable across CreateSilence and UpdateSilence.
type SilenceSpec struct {
	Matchers  []Matcher
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	Comment   string
}
