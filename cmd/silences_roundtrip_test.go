// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/vanilla"
	"github.com/wilfriedroset/a10r/internal/config"
)

const roundTripSilenceJSON = `{
  "id": "sil-1",
  "status": {"state": "active"},
  "createdBy": "ali\tce",
  "comment": "line1\nline2",
  "startsAt": "2026-10-04T09:00:00Z",
  "endsAt": "2026-10-04T13:00:00Z",
  "matchers": [{"name": "team", "value": "a\tb", "isRegex": false, "isEqual": true}]
}`

type postedSilence struct {
	CreatedBy string `json:"createdBy"`
	Comment   string `json:"comment"`
	Matchers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"matchers"`
}

// roundTripServer serves roundTripSilenceJSON on GET and records the
// body of every POST, which is how Alertmanager takes both a create
// and an update.
func roundTripServer(t *testing.T) (*httptest.Server, *[]postedSilence) {
	t.Helper()
	var posted []postedSilence
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/silence/sil-1":
			_, _ = io.WriteString(w, roundTripSilenceJSON)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/silences":
			var p postedSilence
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			posted = append(posted, p)
			_, _ = io.WriteString(w, `{"silenceID":"sil-1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &posted
}

func TestSilenceWrites_KeepRemoteTextVerbatim(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		run           func(cfg *config.Config, build func(config.Backend) (backend.Client, error)) error
		wantCreatedBy string
	}{
		{
			name: "update",
			run: func(cfg *config.Config, build func(config.Backend) (backend.Client, error)) error {
				var out, errOut bytes.Buffer
				return silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
					silenceUpdateOptions{Ends: "8h"}, "")
			},
			wantCreatedBy: "ali\tce",
		},
		{
			name: "recreate",
			run: func(cfg *config.Config, build func(config.Backend) (backend.Client, error)) error {
				var out, errOut bytes.Buffer
				return silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
					silenceRecreateOptions{Ends: "2h"}, "bob", "")
			},
			wantCreatedBy: "bob",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, posted := roundTripServer(t)
			cfg := cfgWith(config.Backend{Name: "prod"})
			build := func(config.Backend) (backend.Client, error) {
				return vanilla.New(vanilla.ClientConfig{BaseURL: srv.URL, Timeout: 5 * time.Second})
			}

			require.NoError(t, tc.run(cfg, build))
			require.Len(t, *posted, 1)
			got := (*posted)[0]
			require.Equal(t, "line1\nline2", got.Comment, "a multi-line comment must not be flattened")
			require.Equal(t, tc.wantCreatedBy, got.CreatedBy)
			require.Len(t, got.Matchers, 1)
			require.Equal(t, "a\tb", got.Matchers[0].Value,
				"a rewritten matcher value would stop matching the alert")
		})
	}
}
