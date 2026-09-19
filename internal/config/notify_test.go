// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotify_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		notify  Notify
		wantErr string
	}{
		{
			name:   "zero value is valid",
			notify: Notify{},
		},
		{
			name:   "every field set",
			notify: Notify{Enabled: true, Bell: new(false), Desktop: NotifyDesktopBoth, MinSeverity: "critical", Command: []string{"notify-send", "$MESSAGE"}},
		},
		{
			name:   "desktop osc9",
			notify: Notify{Desktop: NotifyDesktopOSC9},
		},
		{
			name:   "desktop off",
			notify: Notify{Desktop: NotifyDesktopOff},
		},
		{
			name:   "desktop osc777",
			notify: Notify{Desktop: NotifyDesktopOSC777},
		},
		{
			name:   "min_severity info",
			notify: Notify{MinSeverity: "info"},
		},
		{
			name:   "min_severity warning",
			notify: Notify{MinSeverity: "warning"},
		},
		{
			name:    "unknown desktop",
			notify:  Notify{Desktop: "toast"},
			wantErr: `tui.notify.desktop must be one of [osc777 osc9 both off] (got "toast")`,
		},
		{
			name:    "desktop is case sensitive",
			notify:  Notify{Desktop: "OSC777"},
			wantErr: `tui.notify.desktop must be one of [osc777 osc9 both off] (got "OSC777")`,
		},
		{
			name:    "unknown min_severity",
			notify:  Notify{MinSeverity: "fatal"},
			wantErr: `tui.notify.min_severity must be one of [critical warning info] (got "fatal")`,
		},
		{
			name:    "min_severity none is not a rank",
			notify:  Notify{MinSeverity: "none"},
			wantErr: `tui.notify.min_severity must be one of [critical warning info] (got "none")`,
		},
		{
			name:    "empty first command element",
			notify:  Notify{Command: []string{"", "$MESSAGE"}},
			wantErr: `tui.notify.command[0]: must not be empty`,
		},
		{
			name:    "whitespace-only first command element",
			notify:  Notify{Command: []string{"  \t "}},
			wantErr: `tui.notify.command[0]: must not be empty`,
		},
		{
			name:    "the placeholder cannot be the program name",
			notify:  Notify{Command: []string{"$MESSAGE"}},
			wantErr: `tui.notify.command[0]: must not be "$MESSAGE"`,
		},
		{
			name:   "the placeholder is legal past the program name",
			notify: Notify{Command: []string{"notify-send", "$MESSAGE"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.notify.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantErr)
		})
	}
}

func TestConfig_ValidateRejectsNotify(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Backends: []Backend{{Name: "prod", URL: "http://am"}},
		TUI:      TUI{Notify: Notify{Desktop: "toast"}},
	}
	require.EqualError(t, cfg.Validate(),
		`tui.notify.desktop must be one of [osc777 osc9 both off] (got "toast")`)
}

func TestNotify_BellOrDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		notify Notify
		want   bool
	}{
		{name: "unset means on", notify: Notify{}, want: true},
		{name: "explicit false", notify: Notify{Bell: new(false)}, want: false},
		{name: "explicit true", notify: Notify{Bell: new(true)}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.notify.BellOrDefault())
		})
	}
}

func TestNotify_DesktopOrDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		notify Notify
		want   string
	}{
		{name: "unset means osc777", notify: Notify{}, want: NotifyDesktopOSC777},
		{
			name:   "unset with a command means off",
			notify: Notify{Command: []string{"notify-send", "$MESSAGE"}},
			want:   NotifyDesktopOff,
		},
		{
			name:   "explicit value wins over the command rule",
			notify: Notify{Desktop: NotifyDesktopBoth, Command: []string{"notify-send"}},
			want:   NotifyDesktopBoth,
		},
		{
			name:   "explicit off stays off",
			notify: Notify{Desktop: NotifyDesktopOff},
			want:   NotifyDesktopOff,
		},
		{name: "explicit osc9", notify: Notify{Desktop: NotifyDesktopOSC9}, want: NotifyDesktopOSC9},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.notify.DesktopOrDefault())
		})
	}
}

func TestNotify_MinSeverityOrDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		notify Notify
		want   string
	}{
		{name: "unset means the pinned default", notify: Notify{}, want: DefaultNotifyMinSeverity},
		{name: "explicit critical", notify: Notify{MinSeverity: "critical"}, want: "critical"},
		{name: "explicit info", notify: Notify{MinSeverity: "info"}, want: "info"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.notify.MinSeverityOrDefault())
		})
	}
}
