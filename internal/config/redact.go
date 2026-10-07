// SPDX-License-Identifier: Apache-2.0

package config

import "net/url"

// RedactURL removes the userinfo from a URL. A common shortcut for
// basic auth is to paste "https://<user>:<password>@host" into the
// url field, and every surface that prints a backend address would
// then print the password. An input that url.Parse rejects is
// returned unchanged: a redactor must not make a malformed config
// look more malformed.
func RedactURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}
