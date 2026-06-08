// SPDX-License-Identifier: Apache-2.0

package matcher

import (
	"sort"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// labelMetricName is Prometheus's synthetic metric-name label. It is
// not a user-facing dimension, so FromLabels never turns it into a
// silence matcher.
const labelMetricName = "__name__"

// FromLabels turns a label set into equality matchers — the matchers
// that identify an alert instance for a silence-one — dropping the
// synthetic `__name__` key. Sorted by name so every caller builds the
// same deterministic matcher slice from the same labels, and a future
// change (different ignored keys, a different operator) lands in one
// place rather than drifting between the TUI prefill and the headless
// silence verbs.
func FromLabels(labels map[string]string) []backend.Matcher {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k == labelMetricName {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]backend.Matcher, 0, len(keys))
	for _, k := range keys {
		out = append(out, backend.Matcher{
			Name: k, Value: labels[k], IsEqual: true,
		})
	}
	return out
}
