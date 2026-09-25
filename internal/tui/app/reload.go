// SPDX-License-Identifier: Apache-2.0

package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// ReloadRequestedMsg asks for the configuration to be re-read and
// the reloadable subset applied. The App refuses or delegates; the
// wiring layer owns the reading, because the loaders and the config
// path live there.
type ReloadRequestedMsg struct{}

// Reload returns a Cmd that requests a configuration reload.
func Reload() tea.Cmd {
	return func() tea.Msg { return ReloadRequestedMsg{} }
}

// reloadConfig refuses an open form, then hands off to the wiring
// layer. A form holds a half-written silence and a reload rebuilds
// the values it was opened against; every other page survives one,
// because the stack, cursors, marks, filters and scope all persist.
func (a *App) reloadConfig() tea.Cmd {
	if a.reload == nil {
		return showFlash(footer.FlashWarn, "reload is unavailable")
	}
	if a.topPageCapturesInput() {
		return showFlash(footer.FlashWarn, "reload: close the form first")
	}
	return a.reload()
}

// ReloadedMsg carries the values a successful reload found, for the
// App to fold into the state the wiring layer cannot reach. The
// wiring layer has already applied everything else.
type ReloadedMsg struct {
	// ThemeName is the reloaded theme.name. The App re-applies it
	// only when it differs from the applied skin, so a reload that
	// left the theme alone does not repaint.
	ThemeName    string
	Tips         bool
	TipsInterval time.Duration
	// ReadOnlyChanged says a read_only flag moved at either layer,
	// which is what the flash names so a user who tightened the
	// setting sees it took rather than reading a bare success.
	ReadOnlyChanged bool
}

// applyReloaded folds a successful reload into the App and always
// reports it, because a reload that prints nothing is
// indistinguishable from a key that did not register.
//
// No "nothing changed" wording: the App sees three settings, while
// the wiring half has already swapped pollers, aliases, key
// overrides, guardrails and column sets it never hears about.
func (a *App) applyReloaded(m ReloadedMsg) tea.Cmd {
	applied := tea.Batch(
		a.forwardToAll(ConfigReloadedMsg{}),
		a.applyReloadedTips(m),
	)
	if skin := a.reloadedSkin(m.ThemeName); skin != "" {
		// Called rather than dispatched, because applySkin's return
		// is the refusal and a success flash batched beside it would
		// take the one flash slot that refusal needs.
		if refused := a.applySkin(skin); refused != nil {
			return tea.Batch(applied, refused)
		}
	}
	if m.ReadOnlyChanged {
		return tea.Batch(applied, showFlash(footer.FlashInfo, "reloaded, read_only applied"))
	}
	return tea.Batch(applied, showFlash(footer.FlashInfo, "reloaded"))
}

// reloadedSkin returns the skin a reload must switch to, or empty
// when it must leave the skin alone.
//
// The auto sentinel is left alone on purpose: the terminal answered
// the background question once at startup, and re-running detection
// would need a fresh round trip whose answer cannot change.
func (a *App) reloadedSkin(themeName string) string {
	if themeName == "" || themeName == theme.AutoSkinName || themeName == a.skinName {
		return ""
	}
	return themeName
}

// applyReloadedTips hands the reloaded tips settings to the bar and
// returns the Cmd that restarts its rotation, nil when the settings
// did not move. Only App.Init otherwise schedules that timer, so a
// bar switched on here and left without its tick would paint one tip
// and never move again.
func (a *App) applyReloadedTips(m ReloadedMsg) tea.Cmd {
	bar, cmd := a.hintbar.Reconfigure(footer.HintBarOptions{
		Enabled:  m.Tips,
		Interval: m.TipsInterval,
	})
	a.hintbar = bar
	return cmd
}
