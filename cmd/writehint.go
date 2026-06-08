// SPDX-License-Identifier: Apache-2.0

package cmd

import "strings"

// Next-step hints (ADR 0045) are undo-primary affordances printed to stderr
// after a successful write, in every output mode. A hint is emitted only
// when the suggested follow-up collapses to a single command; a fan-out
// whose undo would need one command per silence is suppressed rather than
// sprayed.

// createdHint suggests the expire that reverses a create or recreate.
// expire is variadic, so any fan-out of new ids still collapses to one
// command and the hint fires whenever at least one silence was created.
func createdHint(results []writeResult) string {
	ids := successIDs(results)
	if len(ids) == 0 {
		return ""
	}
	return "expire with: a10r silences expire " + strings.Join(ids, " ")
}

// expiredHint suggests the recreate that reverses an expire. recreate takes
// a single id, so the hint fires only when exactly one distinct silence was
// expired (a multi-id expire would need one recreate each — suppressed).
func expiredHint(results []writeResult) string {
	ids := distinctSuccessIDs(results)
	if len(ids) != 1 {
		return ""
	}
	return "recreate with: a10r silences recreate " + ids[0]
}

// updatedHint suggests the get that shows an update's merged result. update
// targets a single id (mirrored across tenants keeps that one id), so the
// follow-up is always a single get.
func updatedHint(results []writeResult) string {
	ids := distinctSuccessIDs(results)
	if len(ids) != 1 {
		return ""
	}
	return "verify with: a10r silences get " + ids[0]
}

// successIDs returns the ids of the non-error results, in order.
func successIDs(results []writeResult) []string {
	var ids []string
	for _, r := range results {
		if r.Status != writeStatusError && r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// distinctSuccessIDs is successIDs with duplicates removed, preserving
// first-seen order (one silence mirrored across tenants shares an id).
func distinctSuccessIDs(results []writeResult) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, id := range successIDs(results) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}
