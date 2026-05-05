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
func SeverityRank(a Alert) int {
	switch strings.ToLower(a.Labels["severity"]) {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}
