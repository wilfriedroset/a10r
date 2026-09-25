// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

func alert(name, severity string, state backend.AlertState) backend.Alert {
	return backend.Alert{
		Labels: map[string]string{"alertname": name, "severity": severity},
		State:  state,
	}
}

func enabled(t *testing.T) *Notifier {
	t.Helper()
	return New(config.Notify{Enabled: true})
}

// flashText resolves cmd and returns the text of the single flash
// message it carries, or "" when it carries none.
func flashText(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	texts := make([]string, 0, 1)
	for _, msg := range resolve(cmd) {
		if f, ok := msg.(footer.FlashShowMsg); ok {
			require.True(t, f.Weak, "a poll is a background event")
			require.Equal(t, footer.FlashWarn, f.Level)
			texts = append(texts, f.Text)
		}
	}
	require.LessOrEqual(t, len(texts), 1, "one poll raises at most one flash")
	if len(texts) == 0 {
		return ""
	}
	return texts[0]
}

// rawText returns the sequences the tea.Raw messages in cmd carry.
func rawText(t *testing.T, cmd tea.Cmd) []string {
	t.Helper()
	var out []string
	for _, msg := range resolve(cmd) {
		raw, ok := msg.(tea.RawMsg)
		if !ok {
			continue
		}
		seq, ok := raw.Msg.(string)
		require.True(t, ok, "a raw message carries its sequence as a string")
		out = append(out, seq)
	}
	return out
}

// resolve flattens a Cmd into the messages it produces, walking one
// level of tea.Batch.
func resolve(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	out := make([]tea.Msg, 0, len(batch))
	for _, c := range batch {
		out = append(out, resolve(c)...)
	}
	return out
}

func TestDisabledNotifierAnnouncesNothing(t *testing.T) {
	t.Parallel()
	n := New(config.Notify{})
	require.NotNil(t, n, "a disabled notifier still exists, so a reload can switch it on")
	require.Nil(t, n.Observe("prod", nil))
	require.Nil(t, n.Observe("prod", []backend.Alert{alert("A", "critical", backend.AlertStateActive)}))
	n.SetScope("prod")
}

func TestApplyReplacesSettings(t *testing.T) {
	t.Parallel()
	n := New(config.Notify{})
	var gotArgs []string
	n.run = func(_ context.Context, _ string, args ...string) error {
		gotArgs = args
		return nil
	}

	n.Apply(config.Notify{
		Enabled:     true,
		MinSeverity: "critical",
		Bell:        new(false),
		Desktop:     config.NotifyDesktopOSC9,
		Command:     []string{"notify-send", config.NotifyMessagePlaceholder},
	})
	require.Nil(t, n.Observe("prod", nil))
	cmd := n.Observe("prod", []backend.Alert{
		alert("PageMe", "critical", backend.AlertStateActive),
		alert("Chatter", "warning", backend.AlertStateActive),
	})

	require.Equal(t, "prod: PageMe (critical)", flashText(t, cmd), "min_severity follows Apply")
	require.Equal(t, []string{desktopSequence(config.NotifyDesktopOSC9, "prod: PageMe (critical)")}, rawText(t, cmd),
		"desktop follows Apply and bell: false rings nothing")
	require.Equal(t, []string{"prod: PageMe (critical)"}, gotArgs, "command follows Apply")
}

// A notifier switched on mid-session must warm up again rather than
// announce every alert that was already firing before the reload.
func TestApplyClearsSeen(t *testing.T) {
	t.Parallel()

	n := enabled(t)
	firing := []backend.Alert{alert("HighLatency", "critical", backend.AlertStateActive)}
	require.Nil(t, n.Observe("prod", nil))
	n.Apply(config.Notify{})
	require.Nil(t, n.Observe("prod", firing), "an off notifier records nothing")

	n.Apply(config.Notify{Enabled: true})

	require.Empty(t, flashText(t, n.Observe("prod", firing)), "the first poll after switching on only seeds")
}

// A reload that leaves the feature on must not drop the sets, or the
// alert that started firing across the reload is seeded, not announced.
func TestApplyKeepsSeenWhileOn(t *testing.T) {
	t.Parallel()

	n := enabled(t)
	require.Nil(t, n.Observe("prod", nil))

	n.Apply(config.Notify{Enabled: true, MinSeverity: "warning"})

	cmd := n.Observe("prod", []backend.Alert{alert("HighLatency", "critical", backend.AlertStateActive)})
	require.Equal(t, "prod: HighLatency (critical)", flashText(t, cmd))
}

func TestObserveDiff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		polls [][]backend.Alert
		want  string
	}{
		{
			name:  "first poll seeds and stays quiet",
			polls: [][]backend.Alert{{alert("HighLatency", "critical", backend.AlertStateActive)}},
			want:  "",
		},
		{
			name: "a new key fires",
			polls: [][]backend.Alert{
				{},
				{alert("HighLatency", "critical", backend.AlertStateActive)},
			},
			want: "prod-eu: HighLatency (critical)",
		},
		{
			name: "a returning key stays quiet",
			polls: [][]backend.Alert{
				{alert("HighLatency", "critical", backend.AlertStateActive)},
				{alert("HighLatency", "critical", backend.AlertStateActive)},
			},
			want: "",
		},
		{
			name: "a suppressed alert is not firing",
			polls: [][]backend.Alert{
				{},
				{alert("HighLatency", "critical", backend.AlertStateSuppressed)},
			},
			want: "",
		},
		{
			name: "an unprocessed alert is not firing",
			polls: [][]backend.Alert{
				{},
				{alert("HighLatency", "critical", backend.AlertStateUnprocessed)},
			},
			want: "",
		},
		{
			name: "a severity below the floor stays quiet",
			polls: [][]backend.Alert{
				{},
				{alert("DiskChatter", "info", backend.AlertStateActive)},
			},
			want: "",
		},
		{
			name: "a below-floor key that later rises stays quiet because it is not new",
			polls: [][]backend.Alert{
				{},
				{alert("DiskChatter", "info", backend.AlertStateActive)},
				{alert("DiskChatter", "critical", backend.AlertStateActive)},
			},
			want: "",
		},
		{
			name: "the group takes the worst severity of its instances",
			polls: [][]backend.Alert{
				{},
				{
					alert("HighLatency", "info", backend.AlertStateActive),
					alert("HighLatency", "critical", backend.AlertStateActive),
				},
			},
			want: "prod-eu: HighLatency (critical)",
		},
		{
			name: "several new keys batch into one message",
			polls: [][]backend.Alert{
				{},
				{
					alert("HighLatency", "warning", backend.AlertStateActive),
					alert("DiskFull", "critical", backend.AlertStateActive),
					alert("NodeDown", "warning", backend.AlertStateActive),
				},
			},
			want: "prod-eu: 3 new firing alerts, worst critical",
		},
		{
			name: "a key that clears and returns fires again",
			polls: [][]backend.Alert{
				{alert("HighLatency", "critical", backend.AlertStateActive)},
				{},
				{alert("HighLatency", "critical", backend.AlertStateActive)},
			},
			want: "prod-eu: HighLatency (critical)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := New(config.Notify{Enabled: true})
			var last tea.Cmd
			for _, alerts := range tc.polls {
				last = n.Observe("prod-eu", alerts)
			}
			require.Equal(t, tc.want, flashText(t, last))
		})
	}
}

func TestObserveWarmsUpPerTenant(t *testing.T) {
	n := enabled(t)
	firing := []backend.Alert{alert("HighLatency", "critical", backend.AlertStateActive)}
	require.Empty(t, flashText(t, n.Observe("prod-eu", firing)))
	require.Empty(t, flashText(t, n.Observe("prod-us", firing)), "each tenant warms up on its own")
	require.Empty(t, flashText(t, n.Observe("prod-eu", firing)))
}

func TestObserveHonoursMinSeverity(t *testing.T) {
	n := New(config.Notify{Enabled: true, MinSeverity: "critical"})
	require.Nil(t, n.Observe("prod-eu", nil))
	cmd := n.Observe("prod-eu", []backend.Alert{
		alert("PageMe", "critical", backend.AlertStateActive),
		alert("Chatter", "warning", backend.AlertStateActive),
	})
	require.Equal(t, "prod-eu: PageMe (critical)", flashText(t, cmd))
}

func TestScope(t *testing.T) {
	firing := []backend.Alert{alert("HighLatency", "critical", backend.AlertStateActive)}

	t.Run("an out-of-scope tenant never notifies and is not seeded", func(t *testing.T) {
		n := enabled(t)
		n.SetScope("prod-eu")
		require.Nil(t, n.Observe("prod-us", nil))
		require.Nil(t, n.Observe("prod-us", firing))
		require.NotContains(t, n.seen, "prod-us")
	})

	t.Run("a tenant that leaves and returns warms up again", func(t *testing.T) {
		n := enabled(t)
		require.Empty(t, flashText(t, n.Observe("prod-eu", nil)))
		n.SetScope("prod-us")
		require.NotContains(t, n.seen, "prod-eu")
		n.SetScope("all")
		require.Empty(t, flashText(t, n.Observe("prod-eu", firing)), "the tenant warms up again")
		require.Empty(t, flashText(t, n.Observe("prod-eu", firing)))
	})

	t.Run("a tenant in scope keeps its set", func(t *testing.T) {
		n := enabled(t)
		require.Empty(t, flashText(t, n.Observe("prod-eu", nil)))
		n.SetScope("prod-eu,prod-us")
		require.Equal(t, "prod-eu: HighLatency (critical)", flashText(t, n.Observe("prod-eu", firing)))
	})
}

func TestFormatBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		tenant string
		names  []string
		worst  int
		want   string
	}{
		{name: "one alert names it", tenant: "prod-eu", names: []string{"HighLatency"}, worst: 3, want: "prod-eu: HighLatency (critical)"},
		{name: "one alert at warning", tenant: "prod-eu", names: []string{"DiskFull"}, worst: 2, want: "prod-eu: DiskFull (warning)"},
		{name: "several alerts count and rank", tenant: "prod-eu", names: []string{"A", "B"}, worst: 2, want: "prod-eu: 2 new firing alerts, worst warning"},
		{
			name:   "a hostile tenant name cannot break the line",
			tenant: "prod\neu;",
			names:  []string{"X"},
			worst:  3,
			want:   "prod eu : X (critical)",
		},
		{
			name:   "a hostile alertname cannot break the line",
			tenant: "prod-eu",
			names:  []string{"X\x1b]9;own"},
			worst:  3,
			want:   "prod-eu: X ]9 own (critical)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, formatBody(tc.tenant, tc.names, tc.worst))
		})
	}
}

func TestSanitize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text passes through", in: "prod-eu: HighLatency", want: "prod-eu: HighLatency"},
		{name: "a semicolon cannot close the sequence", in: "a;b", want: "a b"},
		{name: "the bell cannot close the sequence", in: "a\ab", want: "a b"},
		{name: "an escape cannot start its own sequence", in: "a\x1b]9;x\ab", want: "a ]9 x b"},
		{name: "a newline becomes a space", in: "a\nb", want: "a b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, sanitize(tc.in))
		})
	}
}

func TestDesktopSequence(t *testing.T) {
	t.Parallel()
	const esc = "\x1b"
	tests := []struct {
		name    string
		desktop string
		want    string
	}{
		{name: "osc777 carries title and body", desktop: config.NotifyDesktopOSC777, want: esc + "]777;notify;a10r;prod: X (critical)\a"},
		{name: "osc9 carries the body only", desktop: config.NotifyDesktopOSC9, want: esc + "]9;prod: X (critical)\a"},
		{
			name:    "both emits the two sequences",
			desktop: config.NotifyDesktopBoth,
			want:    esc + "]777;notify;a10r;prod: X (critical)\a" + esc + "]9;prod: X (critical)\a",
		},
		{name: "off emits nothing", desktop: config.NotifyDesktopOff, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, desktopSequence(tc.desktop, "prod: X (critical)"))
		})
	}
}

func TestDesktopSequenceSanitisesTheBody(t *testing.T) {
	t.Parallel()
	got := desktopSequence(config.NotifyDesktopOSC9, "evil;\x1b]9;own\a")
	require.Equal(t, "\x1b]9;evil  ]9 own \a", got)
}

func TestEmitWritesOneRawMessage(t *testing.T) {
	t.Parallel()
	const body = "prod-eu: X (critical)"
	osc777 := "\x1b]777;notify;a10r;" + body + bell
	osc9 := "\x1b]9;" + body + bell
	tests := []struct {
		name    string
		desktop string
		bell    bool
		want    []string
	}{
		{name: "osc777 then the bell", desktop: config.NotifyDesktopOSC777, bell: true, want: []string{osc777 + bell}},
		{name: "osc9 then the bell", desktop: config.NotifyDesktopOSC9, bell: true, want: []string{osc9 + bell}},
		{name: "both sequences then the bell", desktop: config.NotifyDesktopBoth, bell: true, want: []string{osc777 + osc9 + bell}},
		{name: "off rings the bell alone", desktop: config.NotifyDesktopOff, bell: true, want: []string{bell}},
		{name: "a silent bell leaves the sequence alone", desktop: config.NotifyDesktopOSC777, bell: false, want: []string{osc777}},
		{name: "off and no bell writes nothing raw", desktop: config.NotifyDesktopOff, bell: false, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := New(config.Notify{Enabled: true, Desktop: tc.desktop, Bell: &tc.bell})
			require.Nil(t, n.Observe("prod-eu", nil))
			cmd := n.Observe("prod-eu", []backend.Alert{alert("X", "critical", backend.AlertStateActive)})
			require.Equal(t, tc.want, rawText(t, cmd))
			require.Equal(t, body, flashText(t, cmd), "the flash reaches the user whatever the transport")
		})
	}
}

func TestEmitRingsOnceForAPollOfSeveralAlerts(t *testing.T) {
	t.Parallel()
	const body = "prod-eu: 3 new firing alerts, worst critical"
	n := New(config.Notify{Enabled: true, Desktop: config.NotifyDesktopBoth})
	require.Nil(t, n.Observe("prod-eu", nil))
	cmd := n.Observe("prod-eu", []backend.Alert{
		alert("A", "critical", backend.AlertStateActive),
		alert("B", "warning", backend.AlertStateActive),
		alert("C", "warning", backend.AlertStateActive),
	})
	want := "\x1b]777;notify;a10r;" + body + bell + "\x1b]9;" + body + bell + bell
	require.Equal(t, []string{want}, rawText(t, cmd), "three new alerts raise one message and one bell")
	require.Equal(t, body, flashText(t, cmd))
}

func TestObserveSanitisesTheBodyForEveryTransport(t *testing.T) {
	t.Parallel()
	const want = "prod-eu: Evil Name  ]9 own  (critical)"
	n := enabled(t)
	require.Nil(t, n.Observe("prod-eu", nil))
	cmd := n.Observe("prod-eu", []backend.Alert{alert("Evil\nName;\x1b]9;own\a", "critical", backend.AlertStateActive)})
	require.Equal(t, want, flashText(t, cmd), "a hostile alertname must not break the one-line flash")
	require.Equal(t, []string{"\x1b]777;notify;a10r;" + want + bell + bell}, rawText(t, cmd))
}

func TestSubstituteMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{
			name: "the whole element is replaced",
			argv: []string{"notify-send", "a10r", "$MESSAGE"},
			want: []string{"notify-send", "a10r", "prod: X (critical)"},
		},
		{
			name: "every occurrence is replaced",
			argv: []string{"log", "$MESSAGE", "$MESSAGE"},
			want: []string{"log", "prod: X (critical)", "prod: X (critical)"},
		},
		{
			name: "a partial element is left alone",
			argv: []string{"log", "prefix-$MESSAGE", "${MESSAGE}"},
			want: []string{"log", "prefix-$MESSAGE", "${MESSAGE}"},
		},
		{
			name: "argv without the placeholder is passed as written",
			argv: []string{"beep"},
			want: []string{"beep"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, substituteMessage(tc.argv, "prod: X (critical)"))
		})
	}
}

func TestCommandRunsWithTheConfiguredArgv(t *testing.T) {
	n := New(config.Notify{Enabled: true, Command: []string{"notify-send", "$MESSAGE"}})
	var gotName string
	var gotArgs []string
	n.run = func(_ context.Context, name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	}
	require.Nil(t, n.Observe("prod-eu", nil))
	for _, msg := range resolve(n.Observe("prod-eu", []backend.Alert{alert("X", "critical", backend.AlertStateActive)})) {
		_ = msg
	}
	require.Equal(t, "notify-send", gotName)
	require.Equal(t, []string{"prod-eu: X (critical)"}, gotArgs)
}

func TestCommandFailureFlashesOncePerSession(t *testing.T) {
	n := New(config.Notify{Enabled: true, Command: []string{"notify-send", "$MESSAGE"}})
	n.run = func(context.Context, string, ...string) error { return errors.New("exec: not found") }

	texts := func(alertname string) []string {
		var out []string
		for _, msg := range resolve(n.Observe("prod-eu", []backend.Alert{alert(alertname, "critical", backend.AlertStateActive)})) {
			if f, ok := msg.(footer.FlashShowMsg); ok && strings.Contains(f.Text, "not found") {
				out = append(out, f.Text)
			}
		}
		return out
	}

	require.Nil(t, n.Observe("prod-eu", nil))
	require.Len(t, texts("First"), 1)
	require.Empty(t, texts("Second"), "a missing notifier must not spam")
}

// TestConfigSeverityNamesMatchBackendRanks checks the three severity
// names internal/config/notify.go repeats because that package cannot
// import internal/backend. The list below is a third copy of them, so
// the test catches internal/config dropping a name and does not catch
// internal/backend gaining a rank.
func TestConfigSeverityNamesMatchBackendRanks(t *testing.T) {
	t.Parallel()
	critical := backend.SeverityRank(map[string]string{"severity": "critical"})
	warning := backend.SeverityRank(map[string]string{"severity": "warning"})
	info := backend.SeverityRank(map[string]string{"severity": "info"})

	require.Greater(t, critical, warning)
	require.Greater(t, warning, info)
	require.Positive(t, info)

	for _, name := range []string{"critical", "warning", "info"} {
		require.NoError(t, config.Notify{MinSeverity: name}.Validate(), "config must accept %q", name)
		require.Positive(t, backend.SeverityRank(map[string]string{"severity": name}),
			"backend must rank %q", name)
	}
	require.Error(t, config.Notify{MinSeverity: "page"}.Validate(),
		"config must reject a name backend.SeverityRank does not weigh")
}
