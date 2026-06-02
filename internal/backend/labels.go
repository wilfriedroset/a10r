// SPDX-License-Identifier: Apache-2.0

package backend

import "maps"

// CommonLabels returns the labels that appear with the same value in
// every alert — the key+value intersection across the set. Used to
// surface the labels a group of alerts shares and to prefill a group
// silence with matchers covering exactly that set. Empty input
// returns an empty (non-nil) map so callers iterate without a nil
// check.
func CommonLabels(alerts []Alert) map[string]string {
	if len(alerts) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(alerts[0].Labels))
	maps.Copy(out, alerts[0].Labels)
	for _, a := range alerts[1:] {
		for k, v := range out {
			other, ok := a.Labels[k]
			if !ok || other != v {
				delete(out, k)
			}
		}
	}
	return out
}

// DistinguishingLabels returns the labels in a that aren't shared
// across the set — the keys whose value diverges from (or is absent
// in) the common intersection. Rendered on instance rows so each
// instance identifies itself (instance / pod / host / …) rather than
// echoing the labels already shown once as common.
func DistinguishingLabels(a Alert, common map[string]string) map[string]string {
	out := make(map[string]string, len(a.Labels))
	for k, v := range a.Labels {
		if cv, ok := common[k]; ok && cv == v {
			continue
		}
		out[k] = v
	}
	return out
}
