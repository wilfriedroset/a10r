// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func TestApp_WindowTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		enabled  bool
		readOnly bool
		scope    string
		crumb    string
		want     string
	}{
		{
			name:  "off by default writes no title",
			scope: scopeAll,
			crumb: "alerts",
		},
		{
			name:    "all tenants in scope",
			enabled: true,
			scope:   scopeAll,
			crumb:   "alerts",
			want:    "a10r: all alerts",
		},
		{
			name:    "one tenant in scope",
			enabled: true,
			scope:   "prod",
			crumb:   "silences",
			want:    "a10r: prod silences",
		},
		{
			name:    "a subset collapses to first+N",
			enabled: true,
			scope:   "prod,staging,dev",
			crumb:   "alerts",
			want:    "a10r: prod+2 alerts",
		},
		{
			name:     "read-only appends the badge",
			enabled:  true,
			readOnly: true,
			scope:    scopeAll,
			crumb:    "status",
			want:     "a10r: all status [read-only]",
		},
		{
			name:    "an empty stack still names the scope",
			enabled: true,
			scope:   "prod",
			want:    "a10r: prod",
		},
		{
			name:    "control characters in a tenant name are stripped",
			enabled: true,
			scope:   "pr\x1b]0;pwned\x07od",
			crumb:   "alerts",
			want:    "a10r: pr]0;pwnedod alerts",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := NewApp(Options{
				Styles:        testutil.LoadStyles(t),
				Dispatcher:    keys.New(nil),
				TerminalTitle: tc.enabled,
				ReadOnly:      tc.readOnly,
				Scope:         tc.scope,
			})
			if tc.crumb != "" {
				a.stack = append(a.stack, newFakePage(tc.crumb))
			}
			require.Equal(t, tc.want, a.windowTitle())
		})
	}
}

func TestApp_WindowTitleFollowsScopeChange(t *testing.T) {
	t.Parallel()

	a := NewApp(Options{
		Styles:        testutil.LoadStyles(t),
		Dispatcher:    keys.New(nil),
		TerminalTitle: true,
		Scope:         scopeAll,
	})
	a.stack = append(a.stack, newFakePage("alerts"))
	a.Update(ScopeChangedMsg{Scope: "prod,staging"})

	require.Equal(t, "a10r: prod+1 alerts", a.windowTitle(),
		"a ScopeChangedMsg must move the title, because the scope is "+
			"the App's own state and pages never report it back")
}

func TestApp_ViewCarriesTheWindowTitle(t *testing.T) {
	t.Parallel()

	a := NewApp(Options{
		Styles:        testutil.LoadStyles(t),
		Dispatcher:    keys.New(nil),
		TerminalTitle: true,
		Scope:         scopeAll,
	})
	a.stack = append(a.stack, newFakePage("alerts"))

	require.Empty(t, a.View().WindowTitle,
		"the pre-resize frame paints nothing, so it must not title either")

	a.width, a.height = 80, 24
	require.Equal(t, "a10r: all alerts", a.View().WindowTitle)
}

func TestApp_ViewWritesNoTitleWhenDisabled(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	a.stack = append(a.stack, newFakePage("alerts"))
	a.width, a.height = 80, 24

	require.Empty(t, a.View().WindowTitle,
		"tui.terminal_title defaults to false, so a10r must leave the "+
			"terminal title alone")
}
