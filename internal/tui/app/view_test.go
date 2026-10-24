// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/panel"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

type sizingPage struct {
	*fakePage
	title         string
	width, height int
}

func (p *sizingPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	_, cmd := p.fakePage.Update(msg)
	return p, cmd
}

func (p *sizingPage) View(width, height int) string {
	p.width, p.height = width, height
	return p.fakePage.View(width, height)
}

func (p *sizingPage) Title() string { return p.title }

func TestApp_ViewSplitsTheTerminalBetweenChromeAndBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     string
		promptOpen bool
		tips       bool
		lostRows   int
	}{
		{name: "page only"},
		{name: "header takes a body row", header: "hdr", lostRows: 1},
		{name: "open prompt panel takes three rows", promptOpen: true, lostRows: 3},
		{name: "hint bar takes a footer row", tips: true, lostRows: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := NewApp(Options{
				Styles:     testutil.LoadStyles(t),
				Dispatcher: keys.New(nil),
				HintBar:    footer.NewHintBar(footer.HintBarOptions{Enabled: tc.tips}),
				Session:    testutil.Session(),
			})
			_, _ = a.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
			home := newFakePage("alerts")
			page := &sizingPage{fakePage: newFakePage("detail"), title: "detail(prod)[3]"}
			page.headerLabel = tc.header
			drive(t, a, PushPage(func() Page { return home }))
			drive(t, a, PushPage(func() Page { return page }))
			if tc.promptOpen {
				a.prompt = a.prompt.Open(footer.PromptCommand)
			}

			lines := strings.Split(testutil.StripStyle(a.View().Content), "\n")

			require.Len(t, lines, 20)
			require.Equal(t, 58, page.width)
			require.Equal(t, 20-lipgloss.Height(panel.RenderTop(a.panelState(), a.styles))-3-tc.lostRows, page.height)
			require.Equal(t, "<alerts> <detail>", strings.TrimRight(lines[len(lines)-1], " "))
			require.Contains(t, strings.Join(lines, "\n"), " detail(prod)[3] ")
		})
	}
}

func TestApp_ViewWaitsForBothDimensions(t *testing.T) {
	t.Parallel()
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 0}, {Width: 0, Height: 24}} {
		a := newTestApp(t)
		_, _ = a.Update(size)
		require.Empty(t, a.View().Content)
	}
}

func TestTenantBindings_StopAtNine(t *testing.T) {
	t.Parallel()
	tenants := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"}

	got := tenantBindings(tenants)

	want := make([]panel.TenantBinding, 0, 10)
	want = append(want, panel.TenantBinding{Key: "0", Name: scopeAll})
	for i, name := range tenants[:9] {
		want = append(want, panel.TenantBinding{Key: string(rune('1' + i)), Name: name})
	}
	require.Equal(t, want, got)
}
