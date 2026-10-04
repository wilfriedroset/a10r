// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSanitize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text is untouched", in: "HighCPU on prod", want: "HighCPU on prod"},
		{name: "empty", in: "", want: ""},
		{name: "SGR escape", in: "red \x1b[31mtext", want: "red  [31mtext"},
		{name: "newline", in: "line one\nline two", want: "line one line two"},
		{name: "tab", in: "a\tb", want: "a b"},
		{name: "NUL", in: "a\x00b", want: "a b"},
		{name: "carriage return", in: "erase\rme", want: "erase me"},
		{name: "delete", in: "a\x7fb", want: "a b"},
		{name: "non-ASCII survives", in: "café ✓", want: "café ✓"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, sanitize(tc.in))
		})
	}
}

func TestToAlert_SanitisesRemoteText(t *testing.T) {
	t.Parallel()

	got := toAlert(wireAlert{
		Fingerprint:  "ab\x1bcd",
		Labels:       map[string]string{"alert\tname": "High\x1b[31mCPU"},
		Annotations:  map[string]string{"sum\nmary": "disk\x00full"},
		GeneratorURL: "http://prom/graph\x1b]0;x\a",
		Status:       wireAlertStatus{State: "active"},
	})

	require.Equal(t, map[string]string{"alert name": "High [31mCPU"}, got.Labels)
	require.Equal(t, map[string]string{"sum mary": "disk full"}, got.Annotations)
	require.Equal(t, "http://prom/graph ]0;x ", got.GeneratorURL)
	require.Equal(t, "ab\x1bcd", got.Fingerprint,
		"the fingerprint is an identity compared across polls, never only rendered")
}

func TestToAlert_SanitisesSuppressionListsButNotSilenceIDs(t *testing.T) {
	t.Parallel()

	got := toAlert(wireAlert{Status: wireAlertStatus{
		State:       "suppressed",
		SilencedBy:  []string{"sil\x1b42"},
		InhibitedBy: []string{"Disk\x1b[2JFull"},
		MutedBy:     []string{"time\nwindow"},
	}})

	require.Equal(t, []string{"sil\x1b42"}, got.SilencedBy,
		"the silence id keys the map the alert page joins against")
	require.Equal(t, []string{"Disk [2JFull"}, got.InhibitedBy)
	require.Equal(t, []string{"time window"}, got.MutedBy)
}

func TestToAlert_SanitisesReceiverNames(t *testing.T) {
	t.Parallel()

	got := toAlert(wireAlert{
		Status:    wireAlertStatus{State: "active"},
		Receivers: []wireReceiver{{Name: "page\x1b[2Jrduty"}},
	})
	require.Equal(t, []string{"page [2Jrduty"}, got.Receivers)
}

func TestToAlert_CleanInputKeepsTheDecodedMaps(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"alertname": "HighCPU"}
	got := toAlert(wireAlert{Labels: labels, Status: wireAlertStatus{State: "active"}})
	labels["cluster"] = "eu-1"
	require.Equal(t, "eu-1", got.Labels["cluster"],
		"a clean map is aliased, so every poll of a healthy backend copies nothing")
	require.Nil(t, got.Annotations, "a wire response with no annotations stays nil")
}

func TestToReceiver_SanitisesName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "web hook", toReceiver(wireReceiver{Name: "web\thook"}).Name)
}

func TestToStatus_SanitisesRenderedText(t *testing.T) {
	t.Parallel()

	got := toStatus(wireStatus{
		Cluster: wireClusterStatus{
			Status: "rea\x1bdy",
			Peers:  []wireClusterPeer{{Name: "peer\n1", Address: "10.0.0.1\t:9094"}},
		},
		VersionInfo: wireVersionInfo{Version: "0.28\x1b.0", BuildUser: "root\x00"},
		Config:      wireConfigBlock{Original: "route:\n  receiver: web\n"},
	}, func() time.Time { return time.Unix(0, 0) })

	require.Equal(t, "rea dy", got.Cluster.Status)
	require.Equal(t, "peer 1", got.Cluster.Peers[0].Name)
	require.Equal(t, "10.0.0.1 :9094", got.Cluster.Peers[0].Address)
	require.Equal(t, "0.28 .0", got.Version.Version)
	require.Equal(t, "root ", got.Version.BuildUser)
	require.Equal(t, "route:\n  receiver: web\n", got.Config,
		"the backend config is a document the status page splits on newlines")
}

func TestToSilence_KeepsRemoteTextVerbatim(t *testing.T) {
	t.Parallel()

	got := toSilence(wireSilence{
		ID:        "id42",
		CreatedBy: "al\x1bice",
		Comment:   "on\ncall\twindow",
		Status:    wireSilenceState{State: "active"},
		Matchers:  []wireMatcher{{Name: "clus\x00ter", Value: "eu\t1"}},
	})

	require.Equal(t, "al\x1bice", got.CreatedBy)
	require.Equal(t, "on\ncall\twindow", got.Comment)
	require.Len(t, got.Matchers, 1)
	require.Equal(t, "clus\x00ter", got.Matchers[0].Name)
	require.Equal(t, "eu\t1", got.Matchers[0].Value)
	require.True(t, got.Matchers[0].IsEqual, "a nil isEqual still defaults to the positive form")
}
