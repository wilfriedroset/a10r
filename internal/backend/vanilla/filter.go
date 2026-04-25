// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"net/url"
	"strconv"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// encodeAlertFilter renders a backend.AlertFilter as the url.Values
// /api/v2/alerts expects. Pointer-bool fields collapse to a single
// `key=true|false` only when set; nil leaves the param off so the
// server applies its default. Filter strings repeat as multiple
// `filter=` params per audit §1.4.
func encodeAlertFilter(f backend.AlertFilter) url.Values {
	v := url.Values{}
	addBoolFilter(v, "active", f.Active)
	addBoolFilter(v, "silenced", f.Silenced)
	addBoolFilter(v, "inhibited", f.Inhibited)
	addBoolFilter(v, "unprocessed", f.Unprocessed)
	for _, m := range f.Filter {
		v.Add("filter", m)
	}
	if f.Receiver != "" {
		v.Set("receiver", f.Receiver)
	}
	return v
}

// encodeSilenceFilter renders a backend.SilenceFilter as the url.Values
// /api/v2/silences expects.
func encodeSilenceFilter(f backend.SilenceFilter) url.Values {
	v := url.Values{}
	for _, m := range f.Filter {
		v.Add("filter", m)
	}
	return v
}

func addBoolFilter(v url.Values, key string, val *bool) {
	if val == nil {
		return
	}
	v.Set(key, strconv.FormatBool(*val))
}
