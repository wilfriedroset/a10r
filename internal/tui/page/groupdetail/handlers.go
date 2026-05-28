// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"log/slog"
	"maps"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/alert"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	silencespage "github.com/wilfriedroset/a10r/internal/tui/page/silences"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.BackendStatusMsg:
		p.HandleBackendStatusMsg(m)
		return p, nil
	case poll.DataMsg:
		listpage.ApplyDataMsg(&p.Base, &p.PollingUI, m, func(_ string, alerts []backend.Alert) {
			p.instances = p.matchingInstances(alerts)
		})
		return p, nil
	case spinner.TickMsg:
		if !p.SpinnerActive(p.ScopeIncludes) {
			return p, nil
		}
		var cmd tea.Cmd
		p.Spinner, cmd = p.Spinner.Update(m)
		return p, cmd
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.HandleFilterPrompt(m)
		return p, nil
	case silenceform.SubmittedMsg:
		slog.Default().Info("silence write succeeded",
			slog.String("op", "created"),
			slog.String("id", m.ID),
			slog.String("surface", "groupdetail-form"))
		return p, footer.ShowFlash(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		p.pendingBulkSilence = pendingBulkSilence{}
		return p, nil
	case silenceform.BulkSubmittedMsg:
		cmd := p.handleBulkSilenceSubmit(m)
		return p, cmd
	case modal.ConfirmResultMsg:
		cmd := p.handleBulkSilenceConfirm(m)
		return p, cmd
	case bulkop.DoneMsg[string]:
		cmd := p.handleBulkSilenceDone(m)
		return p, cmd
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	if p.handleMotion(m) {
		return p, nil
	}
	if p.handleSort(m) {
		return p, nil
	}
	return p.handleAction(m)
}

func (p *Page) handleMotion(m tea.KeyPressMsg) bool {
	changed, handled := p.MoveCursor(m.String(), len(p.view))
	if !handled {
		return false
	}
	if changed {
		p.snapshotFocus()
	}
	return true
}

func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	return cursor.HandleSort(
		m.String(),
		p.sorter,
		func() { p.focusFingerprint = "" },
		p.recompute,
	)
}

// handleAction processes the page's per-view action keys. State-
// filter cycling is bound to Shift+F because the app-global `t` /
// `Shift+T` consume their keys before the page sees a bare `t`.
func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "enter":
		cmd := p.drillToDetail()
		return p, cmd
	case "space":
		p.toggleMarkAtCursor()
	case "F":
		p.cycleStateFilter()
		p.recompute()
	case "s":
		if p.readOnly {
			return p, footer.ShowFlash(footer.FlashWarn, hintReadOnly)
		}
		cmd := p.openSilenceForS()
		return p, cmd
	case "S":
		cmd := p.openSilencesView()
		return p, cmd
	case "C":
		p.commonCollapsed = !p.commonCollapsed
		return p, nil
	case "T":
		// Ask the App to flip the app-global state-format density; the
		// page's SetStateFormat hook receives the broadcast result.
		return p, func() tea.Msg { return app.StateFormatToggleMsg{} }
	case "r":
		cmd := p.requestRefresh()
		return p, cmd
	case "w":
		p.toggleWatch()
		return p, nil
	}
	return p, nil
}

func (p *Page) toggleWatch() { listpage.ToggleWatch(&p.Base, &p.PollingUI) }

func (p *Page) requestRefresh() tea.Cmd {
	return listpage.RequestRefresh(&p.Base, &p.PollingUI, "alerts")
}

func (p *Page) toggleMarkAtCursor() {
	listpage.ToggleMarkAtCursor(p.view, p.Index(), p.marks, func(e instanceEntry) string { return e.a.Fingerprint })
}

// handleClearMarks drops every mark on the global Ctrl+\ binding,
// flashing confirmation only when there was something to clear.
func (p *Page) handleClearMarks() tea.Cmd {
	if len(p.marks) == 0 {
		return nil
	}
	p.marks = map[string]struct{}{}
	return footer.ShowFlash(footer.FlashInfo, "marks cleared")
}

// drillToDetail pushes the L3 instance-detail page for the cursor
// instance. Empty view falls through to a soft Info flash.
func (p *Page) drillToDetail() tea.Cmd {
	if p.Index() >= len(p.view) {
		return footer.ShowFlash(footer.FlashInfo, "no instance under the cursor")
	}
	entry := p.view[p.Index()]
	styles := p.styles
	now := p.now
	clients := p.clients
	creator := p.creator
	tf := p.timeFormat
	readOnly := p.readOnly
	bulkConcurrency := p.bulkConcurrency
	logger := p.logger
	bulkCtx := p.bulkCtx
	submitCtx := p.submitCtx
	editorResolver := p.editorResolver
	editorCtx := p.editorCtx
	tenant := p.tenant
	return app.PushPage(func() app.Page {
		return alert.New(alert.Options{
			Alert:           entry.a,
			Tenant:          tenant,
			Styles:          styles,
			Now:             now,
			Clients:         clients,
			Creator:         creator,
			TimeFormat:      tf,
			ReadOnly:        readOnly,
			BulkConcurrency: bulkConcurrency,
			Logger:          logger,
			BulkCtx:         bulkCtx,
			SubmitCtx:       submitCtx,
			EditorResolver:  editorResolver,
			EditorCtx:       editorCtx,
		})
	})
}

// openSilenceForS routes `s`: no marks → silence-one form for the
// cursor instance; with marks → the bulk silence-one fanout.
func (p *Page) openSilenceForS() tea.Cmd {
	if len(p.marks) == 0 {
		return p.openSilenceFormForCursor()
	}
	return p.openBulkSilence()
}

// openSilenceFormForCursor pushes the silence-one form prefilled with
// the cursor instance's full labels (MatchersFromLabels drops the
// synthetic __name__). No scope note — silence-one is exactly-scoped.
func (p *Page) openSilenceFormForCursor() tea.Cmd {
	if len(p.clients) == 0 {
		return footer.ShowFlash(footer.FlashWarn, hintNoWriteableBackend)
	}
	if _, ok := p.clients[p.tenant]; !ok {
		return footer.ShowFlash(footer.FlashWarn, hintNoWriteableBackend)
	}
	if p.Index() >= len(p.view) {
		return footer.ShowFlash(footer.FlashInfo, "no instance under the cursor")
	}
	entry := p.view[p.Index()]
	matchers := silenceform.MatchersFromLabels(entry.a.Labels)
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	clients := p.clients
	tenant := p.tenant
	submitCtx := p.submitCtx
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:   clients,
			Tenant:    tenant,
			Styles:    styles,
			Now:       now,
			Creator:   creator,
			Matchers:  matchers,
			SubmitCtx: submitCtx,
		})
	})
}

// openSilencesView handles `S`: pushes the silences list restricted
// to the union of SilencedBy across ALL instances (dedup, order-
// preserving), titled with the alertname and prefilled from the
// common labels so a "new silence" there covers the shared set. Zero
// silences flashes a soft Info hint.
func (p *Page) openSilencesView() tea.Cmd {
	union := p.silencedByUnion()
	if len(union) == 0 {
		return footer.ShowFlash(footer.FlashInfo, "no silences attached to this alert")
	}
	styles := p.styles
	now := p.now
	clients := p.clients
	creator := p.creator
	editorResolver := p.editorResolver
	timeFormat := p.timeFormat
	bulkConcurrency := p.bulkConcurrency
	logger := p.logger
	readOnly := p.readOnly
	editorCtx := p.editorCtx
	bulkCtx := p.bulkCtx
	submitCtx := p.submitCtx
	tenant := p.tenant
	alertName := p.alertName
	labels := p.commonLabelsCopy()
	return app.PushPage(func() app.Page {
		return silencespage.New(silencespage.Options{
			Styles:          styles,
			Now:             now,
			Clients:         clients,
			Creator:         creator,
			EditorResolver:  editorResolver,
			TimeFormat:      timeFormat,
			BulkConcurrency: bulkConcurrency,
			Logger:          logger,
			ReadOnly:        readOnly,
			EditorCtx:       editorCtx,
			BulkCtx:         bulkCtx,
			SubmitCtx:       submitCtx,
			Tenants:         []string{tenant},
			RestrictIDs:     union,
			AlertName:       alertName,
			AlertLabels:     labels,
		})
	})
}

// silencedByUnion collects the distinct SilencedBy IDs across all
// instances, preserving first-seen order so the restricted view's
// ordering is stable across refreshes.
func (p *Page) silencedByUnion() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, a := range p.instances {
		for _, id := range a.SilencedBy {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// commonLabelsCopy returns a copy of the common-label set so the
// pushed page can keep it without aliasing the live map.
func (p *Page) commonLabelsCopy() map[string]string {
	out := make(map[string]string, len(p.common))
	maps.Copy(out, p.common)
	return out
}

// hintReadOnly is flashed on a Dangerous keypress in read-only mode.
const hintReadOnly = "read-only mode — alerts cannot be silenced"

// hintNoWriteableBackend mirrors the alerts/alert pages' const.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"
