// SPDX-License-Identifier: Apache-2.0

package backend

import "strings"

// SeverityRank assigns a numeric weight to the alert's `severity`
// label so descending sort puts critical (highest rank) first.
// `"critical"` → 3, `"warning"` → 2, `"info"` → 1, anything else
// (including missing) → 0.
//
// Lives on the backend package so multiple UI pages — the alerts
// list, the groups list, anything that wants severity-aware
// ordering — share the same weight table without re-deriving it.
//
// Takes the label map directly rather than a full Alert to skip
// the per-call struct copy. The function only ever read
// a.Labels["severity"]; this signature is the one the comparator
// hot loop wants.
func SeverityRank(labels map[string]string) int {
	switch strings.ToLower(labels["severity"]) {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}
