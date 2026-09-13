// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// validatingFakePage stands in for a list page whose Base reports a
// filter error: it satisfies the two optional seams the app reads —
// the Enter-time validator and the chrome tag.
type validatingFakePage struct {
	*fakePage
	tag error
	err error
}

func (p *validatingFakePage) Update(msg tea.Msg) (Page, tea.Cmd) {
	_, _ = p.fakePage.Update(msg)
	return p, nil
}

func (p *validatingFakePage) FilterError() error { return p.tag }

func (p *validatingFakePage) ValidateFilter(string) error { return p.err }

func TestApp_FilterErrorReplacesModeTagInTitle(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	a = updated.(*App)
	// A realistic `resource(scope)[count]` title: panel.styleTitle
	// segments on that shape, and the warn fragment has to survive it.
	page := &validatingFakePage{fakePage: newFakePage("alerts(prod)[3]"), tag: errors.New("regex: missing closing )")}
	drive(t, a, PushPage(func() Page { return page }))

	a.prompt = a.prompt.Open(footer.PromptFilter)
	for _, r := range "^web(" {
		a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	rendered := a.View().Content
	out := testutil.StripStyle(rendered)
	require.Contains(t, out, "</^web(>")
	require.Contains(t, out, "[regex: missing closing )]",
		"an invalid buffer surfaces the compile reason in the title tag")
	require.NotContains(t, out, "[regex]",
		"the error tag replaces the plain mode tag rather than stacking with it")
	warnSGR, _, _ := strings.Cut(a.styles.Flash.Warn.Render("x"), "x")
	require.Contains(t, rendered, warnSGR+"[regex: missing closing )]",
		"the tag is warn-tinted and survives the title's segment styling intact")

	page.tag = nil
	out = testutil.StripStyle(a.View().Content)
	require.Contains(t, out, "[regex]",
		"a buffer that compiles again falls back to the plain mode tag")
}

func TestApp_FilterPromptAttachesPageValidator(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	a = updated.(*App)
	page := &validatingFakePage{fakePage: newFakePage("alerts"), err: errors.New("missing closing )")}
	drive(t, a, PushPage(func() Page { return page }))

	drive(t, a, a.openPromptCmd(footer.PromptFilter)())
	for _, r := range "^web(" {
		a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, a.prompt.IsOpen(),
		"the page validator gates Enter so a malformed filter stays editable")

	page.err = nil
	a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, a.prompt.IsOpen())
}

// TestApp_FilterPromptWithoutPageValidatorStillSubmits keeps the
// no-op path honest: a page that implements neither seam submits as
// before, and the command prompt is never gated.
func TestApp_FilterPromptWithoutPageValidatorStillSubmits(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	a = updated.(*App)
	drive(t, a, PushPage(func() Page { return newFakePage("alerts") }))

	drive(t, a, a.openPromptCmd(footer.PromptFilter)())
	for _, r := range "^web(" {
		a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, a.prompt.IsOpen())
}
