// SPDX-License-Identifier: Apache-2.0

package alert

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

func stripStyle(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fakeClipboard records every Copy call. Nil err on success;
// callers can flip wantErr to simulate a failure path.
type fakeClipboard struct {
	last    string
	calls   int
	wantErr error
}

func (f *fakeClipboard) Copy(s string) error {
	f.calls++
	f.last = s
	return f.wantErr
}

// fakeBrowser records every Open call.
type fakeBrowser struct {
	last    string
	calls   int
	wantErr error
}

func (f *fakeBrowser) Open(u string) error {
	f.calls++
	f.last = u
	return f.wantErr
}

func sample() backend.Alert {
	return backend.Alert{
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"instance":  "host-1",
		},
		Annotations: map[string]string{
			"summary": "CPU is hot",
		},
		Fingerprint:  "abc123",
		GeneratorURL: "https://example.test/graph?abc",
		State:        backend.AlertStateActive,
		StartsAt:     fixedNow.Add(-5 * time.Minute),
	}
}

func TestPage_RenderShowsAllSections(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Alert:  sample(),
		Tenant: "prod",
		Styles: loadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	out := stripStyle(p.View(120, 30))
	for _, want := range []string{
		"HighCPU", "active", "critical", "abc123",
		"5m ago", "host-1", "CPU is hot",
		"https://example.test/graph?abc", "prod",
	} {
		require.Contains(t, out, want, "missing %q in render", want)
	}
}

func TestPage_HeaderContent(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Alert:  sample(),
		Tenant: "prod",
		Styles: loadStyles(t),
	})
	require.Contains(t, p.HeaderContent(), "HighCPU")
	require.Contains(t, p.HeaderContent(), "active")
	require.Contains(t, p.HeaderContent(), "prod")
}

func TestPage_CopyFingerprintSuccess(t *testing.T) {
	t.Parallel()

	clip := &fakeClipboard{}
	p := New(Options{
		Alert:     sample(),
		Styles:    loadStyles(t),
		Clipboard: clip,
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Equal(t, 1, clip.calls)
	require.Equal(t, "abc123", clip.last)
}

func TestPage_CopyFingerprintWithoutClipboardFlashesWarn(t *testing.T) {
	t.Parallel()

	p := New(Options{Alert: sample(), Styles: loadStyles(t)})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "clipboard")
}

func TestPage_CopyFingerprintErrorFlashesError(t *testing.T) {
	t.Parallel()

	clip := &fakeClipboard{wantErr: errors.New("display server unreachable")}
	p := New(Options{Alert: sample(), Styles: loadStyles(t), Clipboard: clip})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "display server unreachable")
}

func TestPage_OpenURLSuccess(t *testing.T) {
	t.Parallel()

	br := &fakeBrowser{}
	p := New(Options{Alert: sample(), Styles: loadStyles(t), Browser: br})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Equal(t, 1, br.calls)
	require.Equal(t, "https://example.test/graph?abc", br.last)
}

func TestPage_OpenURLMissingIsInfoNoBrowserCall(t *testing.T) {
	t.Parallel()

	a := sample()
	a.GeneratorURL = ""
	br := &fakeBrowser{}
	p := New(Options{Alert: a, Styles: loadStyles(t), Browser: br})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level,
		"missing generator URL must be a soft Info, not an error")
	require.Equal(t, 0, br.calls,
		"browser must NOT be invoked when there's no URL to open")
}

func TestPage_OpenURLErrorFlashesError(t *testing.T) {
	t.Parallel()

	br := &fakeBrowser{wantErr: errors.New("no display server")}
	p := New(Options{Alert: sample(), Styles: loadStyles(t), Browser: br})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "no display server")
}

func TestPage_OpenURLWithoutBrowserFlashesWarn(t *testing.T) {
	t.Parallel()

	p := New(Options{Alert: sample(), Styles: loadStyles(t)})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
}

func TestPage_SilenceFlashesPlaceholder(t *testing.T) {
	t.Parallel()

	p := New(Options{Alert: sample(), Styles: loadStyles(t)})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "silence form")
}

func TestPage_BindingsHaveCopyOpenSilence(t *testing.T) {
	t.Parallel()

	p := New(Options{Alert: sample(), Styles: loadStyles(t)})
	keys := map[string]bool{}
	for _, b := range p.Bindings() {
		keys[b.Key] = true
	}
	require.True(t, keys["s"])
	require.True(t, keys["y"])
	require.True(t, keys["o"])
}

func TestPage_RenderHandlesEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	a := backend.Alert{
		Labels: map[string]string{"alertname": "Bare"},
		State:  backend.AlertStateActive,
	}
	p := New(Options{Alert: a, Styles: loadStyles(t)})
	out := stripStyle(p.View(80, 20))
	require.Contains(t, out, "Bare")
	require.Contains(t, out, "(none)",
		"empty annotations must render as (none) so the section is not blank")
}
