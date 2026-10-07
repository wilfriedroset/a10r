// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/config"
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

// ReloadedMsg reports a reload the wiring layer applied to the
// session. The App reads what it needs from the session itself.
type ReloadedMsg struct {
	// Restart names the settings the new file changed that the
	// running session cannot apply. Empty means the reload landed
	// whole.
	Restart []string
}

// applyReloaded folds a successful reload into the App and always
// reports it, because a reload that prints nothing is
// indistinguishable from a key that did not register.
//
// No "nothing changed" wording: the App does not know what moved, and
// a page on the stack does not need it to, because it reads the
// session.
func (a *App) applyReloaded(m ReloadedMsg) tea.Cmd {
	cfg := a.session.Config()
	a.notify.Apply(a.session.Notify())
	applied := tea.Batch(
		a.forwardToAll(ConfigReloadedMsg{}),
		a.applyReloadedTips(cfg.TUI),
	)
	if skin := a.reloadedSkin(cfg.Theme.Name); skin != "" {
		// Called rather than dispatched, because applySkin's return
		// is the refusal and a success flash batched beside it would
		// take the one flash slot that refusal needs.
		if refused := a.applySkin(skin); refused != nil {
			// configSkin stays put, so the next reload retries.
			return tea.Batch(applied, refused)
		}
	}
	a.configSkin = cfg.Theme.Name
	if len(m.Restart) > 0 {
		return tea.Batch(applied, showFlash(footer.FlashInfo, "reloaded, restart a10r to apply "+strings.Join(m.Restart, ", ")))
	}
	return tea.Batch(applied, showFlash(footer.FlashInfo, "reloaded"))
}

// reloadedSkin returns the skin a reload must switch to, or empty
// when it must leave the skin alone. Only a theme.name the file
// changed counts, because a `:skin` pick lasts the session.
//
// The auto sentinel is left alone on purpose: the terminal answered
// the background question once at startup, and re-running detection
// would need a fresh round trip whose answer cannot change.
func (a *App) reloadedSkin(themeName string) string {
	if themeName == "" || themeName == theme.AutoSkinName || themeName == a.configSkin || themeName == a.skinName {
		return ""
	}
	return themeName
}

// applyReloadedTips hands the reloaded tips settings to the bar and
// returns the Cmd that restarts its rotation, nil when the settings
// did not move. Only App.Init otherwise schedules that timer, so a
// bar switched on here and left without its tick would paint one tip
// and never move again.
func (a *App) applyReloadedTips(tui config.TUI) tea.Cmd {
	bar, cmd := a.hintbar.Reconfigure(footer.HintBarOptions{
		Enabled:  tui.Tips,
		Interval: tui.TipsInterval,
	})
	a.hintbar = bar
	return cmd
}
