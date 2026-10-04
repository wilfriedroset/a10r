// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/session"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// newReloadApp wires an App whose reload callback records that it
// ran, so a test can tell "refused" from "ran and said no".
func newReloadApp(t *testing.T, reload func() tea.Cmd) *App {
	t.Helper()
	return newReloadAppOn(t, reload, testutil.Session())
}

func newReloadAppOn(t *testing.T, reload func() tea.Cmd, sess *session.Session) *App {
	t.Helper()
	// A private copy: testutil.LoadStyles caches one *theme.Styles
	// for the whole process, and applySkin writes through the
	// pointer. Sharing it would let a reload test repaint every
	// parallel test's App mid-render.
	styles := *testutil.LoadStyles(t)
	a := NewApp(Options{
		Styles:     &styles,
		Dispatcher: keys.New(nil),
		Reload:     reload,
		Session:    sess,
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(*App)
}

// A half-typed silence is work the user cannot get back, and a
// reload rebuilds the values a form was opened against. Refusing is
// the only answer that cannot lose the form.
func TestApp_ReloadRefusedWhileAFormIsOpen(t *testing.T) {
	t.Parallel()

	var ran bool
	a := newReloadApp(t, func() tea.Cmd {
		ran = true
		return nil
	})
	form := newFakePage("silence")
	form.capturesInput = true
	drive(t, a, PushPage(func() Page { return form }))

	_, cmd := a.Update(ReloadRequestedMsg{})

	require.False(t, ran, "the config must not be re-read behind an open form")
	require.NotNil(t, cmd)
	require.Equal(t, footer.FlashShowMsg{Level: footer.FlashWarn, Text: "reload: close the form first"}, cmd())
}

// Every other page is safe to reload under: a list page holds no
// unsaved input, and the spec keeps the stack, the cursors and the
// marks across a reload.
func TestApp_ReloadRunsOverAnOrdinaryPage(t *testing.T) {
	t.Parallel()

	var ran bool
	a := newReloadApp(t, func() tea.Cmd {
		ran = true
		return nil
	})
	drive(t, a, PushPage(func() Page { return newFakePage("alerts") }))

	_, _ = a.Update(ReloadRequestedMsg{})

	require.True(t, ran)
}

// A build with no reload wiring must say so rather than look like a
// reload that found nothing to change.
func TestApp_ReloadSaysSoWhenUnwired(t *testing.T) {
	t.Parallel()

	a := newReloadApp(t, nil)

	_, cmd := a.Update(ReloadRequestedMsg{})

	require.NotNil(t, cmd)
	require.Equal(t, footer.FlashShowMsg{Level: footer.FlashWarn, Text: "reload is unavailable"}, cmd())
}

// Only App.Init schedules the hint-bar timer, so a reload that turns
// tips on has to return the restart Cmd itself. Without it the bar
// paints the first tip and never rotates again for the session.
func TestApp_ReloadStartsTheHintBarItTurnedOn(t *testing.T) {
	t.Parallel()

	a := newReloadApp(t, nil)

	cmd := reloadWith(a, config.Config{TUI: config.TUI{Tips: true, TipsInterval: time.Millisecond}})

	require.NotNil(t, cmd)
	require.True(t, a.hintbar.Enabled())
	require.True(t, hasHintBarTick(t, cmd), "an enabled bar without its tick never rotates")
}

// hasHintBarTick reports whether the batch carries the hint bar's
// own timer. The tick type is unexported by footer, so the bar
// itself is asked to recognise it.
func hasHintBarTick(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	bar := footer.NewHintBar(footer.HintBarOptions{Enabled: true})
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		var c tea.Cmd
		c, queue = queue[len(queue)-1], queue[:len(queue)-1]
		if c == nil {
			continue
		}
		msg, ok := runWithBudget(c, 50*time.Millisecond)
		if !ok || msg == nil {
			continue
		}
		if batch, isBatch := msg.(tea.BatchMsg); isBatch {
			queue = append(queue, batch...)
			continue
		}
		if bar.Owns(msg) {
			return true
		}
	}
	return false
}

// A reload that left the tips settings alone must not restart the
// rotation, which would jump the bar back to the first tip and run a
// second timer beside the live one. The unset interval is the case
// that used to look changed every time: the config keeps a zero and
// the bar holds the normalised default.
func TestApp_ReloadLeavesAnUnchangedHintBarAlone(t *testing.T) {
	t.Parallel()

	a := newReloadApp(t, nil)
	tips := config.Config{TUI: config.TUI{Tips: true, TipsInterval: time.Millisecond}}
	_ = reloadWith(a, tips)
	before := a.hintbar

	cmd := reloadWith(a, tips)

	require.Equal(t, before, a.hintbar, "an unchanged bar keeps its rotation cursor and its timer")
	require.False(t, hasHintBarTick(t, cmd), "a second timer beside the live one double-rotates the bar")
	require.Equal(t,
		[]footer.FlashShowMsg{{Level: footer.FlashInfo, Text: "reloaded"}},
		drainFlashes(t, cmd),
	)
}

// The App does not know what moved, so an empty Restart reads as a
// reload that landed whole, never as "nothing changed".
func TestApplyReloadedFlashesReloaded(t *testing.T) {
	t.Parallel()

	a := newReloadApp(t, nil)

	_, cmd := a.Update(ReloadedMsg{})

	require.Equal(t,
		[]footer.FlashShowMsg{{Level: footer.FlashInfo, Text: "reloaded"}},
		drainFlashes(t, cmd),
	)
}

// The settings the session cannot apply are the only thing left for
// the flash to say, and the user needs to know which ones to restart
// for.
func TestApplyReloadedNamesRestartFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		restart []string
		want    string
	}{
		{restart: []string{"tui.remember"}, want: "reloaded, restart a10r to apply tui.remember"},
		{restart: []string{"tui.remember", "tui.other"}, want: "reloaded, restart a10r to apply tui.remember, tui.other"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()

			a := newReloadApp(t, nil)

			_, cmd := a.Update(ReloadedMsg{Restart: tc.restart})

			require.Equal(t,
				[]footer.FlashShowMsg{{Level: footer.FlashInfo, Text: tc.want}},
				drainFlashes(t, cmd))
		})
	}
}

// A reload whose theme.name no longer resolves must leave the user
// with the refusal, not a success flash painted over it one message
// later. Both flashes share one slot, and the last one wins.
func TestApplyReloadedKeepsSkinRefusalFlash(t *testing.T) {
	t.Parallel()

	a := newReloadApp(t, nil)
	a.skinNames = func() []string { return []string{"nord"} }
	a.loadStyles = func(string) (*theme.Styles, error) { return &theme.Styles{}, nil }

	cmd := reloadWith(a, config.Config{Theme: config.Theme{Name: "gone"}})

	flashes := drainFlashes(t, cmd)
	require.Equal(t,
		[]footer.FlashShowMsg{{Level: footer.FlashWarn, Text: `skin "gone" not found`}},
		flashes,
	)
}

// A reload that switched the skin repaints; the name comes from the
// session, which the wiring layer applied before the message.
func TestApplyReloadedSwitchesTheSkin(t *testing.T) {
	t.Parallel()

	a := newReloadApp(t, nil)
	a.skinNames = func() []string { return []string{"nord"} }
	a.loadStyles = func(string) (*theme.Styles, error) { return &theme.Styles{}, nil }

	cmd := reloadWith(a, config.Config{Theme: config.Theme{Name: "nord"}})

	require.Equal(t, "nord", a.skinName)
	require.Equal(t,
		[]footer.FlashShowMsg{{Level: footer.FlashInfo, Text: "reloaded"}},
		drainFlashes(t, cmd))
}

// A `:skin` pick lasts the session, so only a theme.name the file
// changed since the last config may override it on reload. A plain
// step is a reload; a step prefixed with ':' is a `:skin` pick. boot
// is the theme.name the session starts with.
func TestApplyReloadedKeepsTheSessionSkinUntilTheFileChangesIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		boot  string
		steps []string
		want  string
	}{
		{name: "unchanged boot name keeps the pick", boot: "nord", steps: []string{":dracula", "nord"}, want: "dracula"},
		{name: "unchanged name keeps the pick", steps: []string{"nord", ":dracula", "nord"}, want: "dracula"},
		{name: "changed name overrides the pick", steps: []string{"nord", ":dracula", "nord", "monokai"}, want: "monokai"},
		{name: "auto never overrides", steps: []string{theme.AutoSkinName, ":dracula", "nord", theme.AutoSkinName}, want: "nord"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := newReloadAppOn(t, nil, session.New(config.Config{Theme: config.Theme{Name: tc.boot}}))
			a.skinNames = func() []string { return []string{"nord", "dracula", "monokai"} }
			a.loadStyles = func(string) (*theme.Styles, error) { return &theme.Styles{}, nil }
			for _, step := range tc.steps {
				if pick, ok := strings.CutPrefix(step, ":"); ok {
					_, _ = a.Update(ApplySkinMsg{Name: pick})
					continue
				}
				reloadWith(a, config.Config{Theme: config.Theme{Name: step}})
			}

			require.Equal(t, tc.want, a.skinName)
		})
	}
}

// A refused switch is not handled: once the user fixes the skin file,
// reloading the same theme.name must retry it.
func TestApplyReloadedRetriesARefusedSkin(t *testing.T) {
	t.Parallel()

	a := newReloadApp(t, nil)
	a.skinNames = func() []string { return []string{"foo"} }
	broken := true
	a.loadStyles = func(string) (*theme.Styles, error) {
		if broken {
			return nil, errors.New("broken")
		}
		return &theme.Styles{}, nil
	}
	cfg := config.Config{Theme: config.Theme{Name: "foo"}}
	reloadWith(a, cfg)
	require.NotEqual(t, "foo", a.skinName)

	broken = false
	cmd := reloadWith(a, cfg)

	require.Equal(t, "foo", a.skinName)
	require.Equal(t,
		[]footer.FlashShowMsg{{Level: footer.FlashInfo, Text: "reloaded"}},
		drainFlashes(t, cmd))
}

// reloadWith applies cfg to the App's session the way the wiring layer
// does before it hands the App the message, and returns the App's Cmd.
func reloadWith(a *App, cfg config.Config) tea.Cmd {
	a.session.Apply(cfg)
	_, cmd := a.Update(ReloadedMsg{})
	return cmd
}

// drainFlashes runs a Cmd tree and collects every flash it emits, in
// the order a batch delivers them.
func drainFlashes(t *testing.T, cmd tea.Cmd) []footer.FlashShowMsg {
	t.Helper()
	var out []footer.FlashShowMsg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		var c tea.Cmd
		c, queue = queue[0], queue[1:]
		if c == nil {
			continue
		}
		msg, ok := runWithBudget(c, 50*time.Millisecond)
		if !ok || msg == nil {
			continue
		}
		switch m := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, m...)
		case footer.FlashShowMsg:
			out = append(out, m)
		}
	}
	return out
}

// The help overlay is composed on every `?` press and the window
// title is rebuilt every frame, so both read the session rather than
// a copy taken at boot, and an Apply reaches them with no message.
func TestApp_ChromeReadsReadOnlyFromTheSession(t *testing.T) {
	t.Parallel()

	sess := session.New(config.Config{TUI: config.TUI{TerminalTitle: true}})
	a := NewApp(Options{Styles: testutil.LoadStyles(t), Dispatcher: keys.New(nil), Session: sess})
	require.NotContains(t, a.windowTitle(), "[read-only]")

	sess.Apply(config.Config{Defaults: config.Defaults{ReadOnly: true}, TUI: config.TUI{TerminalTitle: true}})

	require.Contains(t, a.windowTitle(), "[read-only]")
}

// A reload has to reach the page the user is looking at and the pages
// below it, which the user walks back to with Esc.
func TestApplyReloaded_ReachesEveryPageOnTheStack(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	home, detail := newFakePage("alerts"), newFakePage("alert")
	drive(t, a, PushPage(func() Page { return home }))
	drive(t, a, PushPage(func() Page { return detail }))

	_, _ = a.Update(ReloadedMsg{})

	for _, p := range []*fakePage{home, detail} {
		require.Contains(t, *p.updateLog, ConfigReloadedMsg{},
			"page %q must hear the reload", p.name)
	}
}
