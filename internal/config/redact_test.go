// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "user and password",
			raw:  "https://__PK_BASICAUTH_1a15d1cf67f9__@am.internal/alertmanager",
			want: "https://am.internal/alertmanager",
		},
		{
			name: "user only",
			raw:  "https://alice@am.internal",
			want: "https://am.internal",
		},
		{
			name: "port survives",
			raw:  "http://__PK_BASICAUTH_1a15d1cf67f9__@am.internal:9093/prefix",
			want: "http://am.internal:9093/prefix",
		},
		{
			name: "no userinfo is untouched",
			raw:  "https://am.internal:9093/alertmanager",
			want: "https://am.internal:9093/alertmanager",
		},
		{
			name: "empty stays empty",
			raw:  "",
			want: "",
		},
		{
			name: "unparseable round-trips",
			raw:  "://not a url\x7f",
			want: "://not a url\x7f",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, RedactURL(tc.raw))
		})
	}
}
