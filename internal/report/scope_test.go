// SPDX-License-Identifier: Apache-2.0

package report_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/report"
	"github.com/wilfriedroset/a10r/internal/uistate"
)

// The report prints one blank line for every reason a scope is not
// worth naming, so the helper must fold all of them to empty. A
// remembered tenant the config dropped is the interesting one: boot
// prunes it away before it can open, and a report that still named it
// would send the operator after a backend that is gone.
func TestRememberedScope(t *testing.T) {
	t.Parallel()

	remembering := &config.Config{
		Backends: []config.Backend{{Name: "prod", URL: "http://x"}},
		TUI:      config.TUI{Remember: true},
	}

	tests := []struct {
		name   string
		cfg    *config.Config
		stored string
		want   string
	}{
		{name: "no config", cfg: nil, stored: "prod", want: ""},
		{
			name:   "remember off",
			cfg:    &config.Config{Backends: remembering.Backends},
			stored: "prod",
			want:   "",
		},
		{name: "nothing stored", cfg: remembering, stored: "", want: ""},
		{name: "pruned to all", cfg: remembering, stored: "gone", want: ""},
		{name: "configured tenant", cfg: remembering, stored: "prod", want: "prod"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, uistate.FileName),
				[]byte("scope: "+tc.stored+"\n"), 0o600,
			))
			store := uistate.Open(dir)
			t.Cleanup(func() { require.NoError(t, store.Close()) })

			require.Equal(t, tc.want, report.RememberedScope(tc.cfg, store))
		})
	}
}
