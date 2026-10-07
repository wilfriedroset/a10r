// SPDX-License-Identifier: Apache-2.0

// Package silencetest drives a pushed silence form from a page test.
// It lives outside the form package because the form's own tests are
// in-package, so a helper there could not import pagetest without a
// cycle.
package silencetest

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
)

// SubmitModal is Submit for the case where the submit is expected to
// open a modal, and it returns that modal.
func SubmitModal(tb testing.TB, cmd tea.Cmd, matchers string) modal.Modal {
	tb.Helper()
	return pagetest.OpenedModal(tb, Submit(tb, cmd, matchers))
}

// Submit pushes the form a page command carries, fills what a submit
// needs, and returns the command the submit produced. matchers is typed
// into the matcher buffer first and is empty when the page prefills it,
// so the submit reaches the write policy rather than stopping at field
// validation.
func Submit(tb testing.TB, cmd tea.Cmd, matchers string) tea.Cmd {
	tb.Helper()
	form, ok := pagetest.PushedPage(tb, cmd).(*silenceform.Form)
	if !ok {
		tb.Fatal("expected the silence form")
	}
	typeInto(form, matchers)
	// The comment is the other field a submit refuses to go without,
	// and it sits four Tab stops below the matchers. Four is a fixed
	// walk because the form exports no way to read or set focus, so
	// this helper only serves forms that open on the matchers: from a
	// FocusEnds form (the recreate path) the same walk overshoots the
	// comment by two stops, so that path must drive its own submit.
	for range 4 {
		_, _ = form.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	typeInto(form, "ack")
	_, submit := form.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	return submit
}

func typeInto(form *silenceform.Form, text string) {
	for _, r := range text {
		_, _ = form.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}
