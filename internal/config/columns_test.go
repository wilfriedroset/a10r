// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestColumns_ValidateRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "empty label",
			cfg:     alertsColumns(Column{Label: "  "}),
			wantErr: `pages.alerts.columns[0].label: must not be empty`,
		},
		{
			name:    "duplicate label",
			cfg:     alertsColumns(Column{Label: "cluster"}, Column{Label: "cluster"}),
			wantErr: `pages.alerts.columns[1].label: "cluster" is already declared on the alerts page`,
		},
		{
			name:    "sort key lowercase",
			cfg:     alertsColumns(Column{Label: "cluster", SortKey: "l"}),
			wantErr: `pages.alerts.columns[0].sort_key: "l" must be one uppercase ASCII letter`,
		},
		{
			name:    "sort key two letters",
			cfg:     alertsColumns(Column{Label: "cluster", SortKey: "LM"}),
			wantErr: `pages.alerts.columns[0].sort_key: "LM" must be one uppercase ASCII letter`,
		},
		{
			name:    "sort key digit",
			cfg:     alertsColumns(Column{Label: "cluster", SortKey: "1"}),
			wantErr: `pages.alerts.columns[0].sort_key: "1" must be one uppercase ASCII letter`,
		},
		{
			name:    "sort key collides with a built-in",
			cfg:     alertsColumns(Column{Label: "cluster", SortKey: "S"}),
			wantErr: `pages.alerts.columns[0].sort_key: "S" is already bound on the alerts page`,
		},
		{
			name:    "sort key collides with the wide toggle",
			cfg:     alertsColumns(Column{Label: "cluster", SortKey: "W"}),
			wantErr: `pages.alerts.columns[0].sort_key: "W" is already bound on the alerts page`,
		},
		{
			name:    "sort key collides with the jump-to-bottom motion",
			cfg:     alertsColumns(Column{Label: "cluster", SortKey: "G"}),
			wantErr: `pages.alerts.columns[0].sort_key: "G" is already bound on the alerts page`,
		},
		{
			name: "sort key used twice",
			cfg: alertsColumns(
				Column{Label: "cluster", SortKey: "L"},
				Column{Label: "pod", SortKey: "L"},
			),
			wantErr: `pages.alerts.columns[1].sort_key: "L" is already bound on the alerts page`,
		},
		{
			name:    "width below the floor",
			cfg:     alertsColumns(Column{Label: "cluster", Width: 2}),
			wantErr: `pages.alerts.columns[0].width: 2 is below the minimum of 3`,
		},
		{
			name:    "title shadows a built-in",
			cfg:     alertsColumns(Column{Label: "cluster", Title: "count"}),
			wantErr: `pages.alerts.columns[0].title: "COUNT" is a built-in column title on the alerts page`,
		},
		{
			name: "group detail is validated too",
			cfg: Config{Pages: PageOverrides{
				GroupDetail: GroupDetailConfig{Columns: []Column{{Label: "pod", Title: "instance"}}},
			}},
			wantErr: `pages.group_detail.columns[0].title: "INSTANCE" is a built-in column title on the group detail page`,
		},
		{
			name: "group detail sort key collides with the silences verb",
			cfg: Config{Pages: PageOverrides{
				GroupDetail: GroupDetailConfig{Columns: []Column{{Label: "pod", SortKey: "S"}}},
			}},
			wantErr: `pages.group_detail.columns[0].sort_key: "S" is already bound on the group detail page`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.Validate()
			require.Error(t, err)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestColumns_ValidateAccepts(t *testing.T) {
	t.Parallel()

	cfg := Config{Pages: PageOverrides{
		Alerts: AlertsPageConfig{Columns: []Column{
			{Label: "cluster", Title: "CLUSTER", SortKey: "L", Width: 12},
			{Label: "pod", Wide: true},
			{Label: "tenant", Title: "TEAM"},
		}},
		GroupDetail: GroupDetailConfig{Columns: []Column{
			{Label: "pod", SortKey: "P", Wide: true},
		}},
	}}
	require.NoError(t, cfg.Validate())
}

// A label column named `tenant` is legal even though the alerts page
// renders a synthetic TENANT column: the user column shows the label
// value, the built-in shows the backend name (ADR 0048).
func TestColumns_TenantLabelNeedsItsOwnTitle(t *testing.T) {
	t.Parallel()

	shadowed := alertsColumns(Column{Label: "tenant", Title: "TENANT"})
	require.EqualError(t, shadowed.Validate(),
		`pages.alerts.columns[0].title: "TENANT" is a built-in column title on the alerts page`)

	renamed := alertsColumns(Column{Label: "tenant", Title: "TEAM"})
	require.NoError(t, renamed.Validate())
}

func TestColumns_DefaultTitleIsTheUpperCasedLabel(t *testing.T) {
	t.Parallel()

	require.Equal(t, "CLUSTER", Column{Label: "cluster"}.TitleOrDefault())
	require.Equal(t, "TEAM", Column{Label: "cluster", Title: "team"}.TitleOrDefault())
}

func TestColumns_UnknownFieldIsRejected(t *testing.T) {
	t.Parallel()

	_, err := decodeStrict([]byte("pages:\n  alerts:\n    columns:\n      - label: cluster\n        colour: red\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "line 5: field colour not found")
}

func TestColumns_RoundTrip(t *testing.T) {
	t.Parallel()

	in := alertsColumns(Column{Label: "cluster", Title: "CLUSTER", SortKey: "L", Width: 12, Wide: true})
	out, err := yaml.Marshal(in)
	require.NoError(t, err)

	var round Config
	require.NoError(t, yaml.Unmarshal(out, &round))
	require.Equal(t, in, round)
}

// Columns declared in a drop-in must survive the merge. The whole
// list is last-wins per page: a fragment that declares any column
// owns the page's column set.
func TestColumns_DropInReplacesTheList(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a10r.yaml"), []byte(`
backends:
  - name: prod
    url: https://am.example
pages:
  alerts:
    poll_interval: 5s
    columns:
      - label: cluster
`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "config.d"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.d", "10-cols.yaml"), []byte(`
pages:
  alerts:
    columns:
      - label: namespace
        wide: true
  group_detail:
    columns:
      - label: pod
`), 0o600))

	cfg, err := Load(LoadOpts{Dir: dir})
	require.NoError(t, err)
	require.Equal(t, []Column{{Label: "namespace", Wide: true}}, cfg.Pages.Alerts.Columns)
	require.Equal(t, []Column{{Label: "pod"}}, cfg.Pages.GroupDetail.Columns)
	require.Equal(t, 5*time.Second, cfg.Pages.Alerts.PollInterval,
		"a drop-in that sets only columns must not clear the base poll interval")
}

// A drop-in that declares no column leaves the base list alone.
func TestColumns_DropInWithoutColumnsKeepsTheBase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a10r.yaml"), []byte(`
backends:
  - name: prod
    url: https://am.example
pages:
  alerts:
    columns:
      - label: cluster
`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "config.d"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.d", "10-poll.yaml"),
		[]byte("pages:\n  alerts:\n    poll_interval: 9s\n"), 0o600))

	cfg, err := Load(LoadOpts{Dir: dir})
	require.NoError(t, err)
	require.Equal(t, []Column{{Label: "cluster"}}, cfg.Pages.Alerts.Columns)
	require.Equal(t, 9*time.Second, cfg.Pages.Alerts.PollInterval)
}

// ADR 0048 keeps user columns off the silences page, so the key must
// not decode there at all.
func TestColumns_RejectedOnPagesThatHaveNone(t *testing.T) {
	t.Parallel()

	for _, page := range []string{"silences", "receivers", "status"} {
		t.Run(page, func(t *testing.T) {
			t.Parallel()
			_, err := decodeStrict([]byte("pages:\n  " + page + ":\n    columns:\n      - label: cluster\n"))
			require.Error(t, err)
			require.Contains(t, err.Error(), "field columns not found")
		})
	}
}

// The group-detail page rides the alerts poll feed, so it must not
// advertise an interval of its own.
func TestColumns_GroupDetailRejectsPollInterval(t *testing.T) {
	t.Parallel()

	_, err := decodeStrict([]byte("pages:\n  group_detail:\n    poll_interval: 5s\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "field poll_interval not found")
}

func TestColumns_RejectsPaddedLabel(t *testing.T) {
	t.Parallel()

	cfg := alertsColumns(Column{Label: "cluster "})
	require.EqualError(t, cfg.Validate(),
		`pages.alerts.columns[0].label: "cluster " must not have leading or trailing whitespace`)
}

func TestColumns_RejectsDuplicateTitle(t *testing.T) {
	t.Parallel()

	cfg := alertsColumns(
		Column{Label: "cluster", Title: "ZONE"},
		Column{Label: "region", Title: "zone"},
	)
	require.EqualError(t, cfg.Validate(),
		`pages.alerts.columns[1].title: "ZONE" is already used by another column on the alerts page`)
}

func alertsColumns(cols ...Column) Config {
	return Config{Pages: PageOverrides{Alerts: AlertsPageConfig{Columns: cols}}}
}
