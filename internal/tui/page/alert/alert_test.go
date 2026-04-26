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

func TestPage_LongNoWhitespaceValueDoesNotFreeze(t *testing.T) {
	t.Parallel()

	// Regression: a 500-char value with NO internal whitespace
	// previously sent wrapHanging into an infinite loop because
	// every iteration's cut landed inside the hanging indent.
	// The render must complete in well under a second.
	a := sample()
	long := strings.Repeat("X", 500)
	a.Annotations = map[string]string{"description": long}
	p := New(Options{Alert: a, Styles: loadStyles(t)})

	done := make(chan string, 1)
	go func() { done <- p.View(80, 30) }()
	select {
	case out := <-done:
		require.NotEmpty(t, out)
	case <-time.After(2 * time.Second):
		t.Fatal("View blocked — wrapHanging likely looped on a no-whitespace value")
	}
}

func TestPage_AnnotationWithEmbeddedNewlinesAlignsAcrossLines(t *testing.T) {
	t.Parallel()

	a := sample()
	a.Annotations = map[string]string{
		// Promql-templated annotation — the description value
		// contains a literal newline between the two facts.
		"description": "VALUE = 0\nLABELS = map[__name__:up cluster:EU]",
	}
	p := New(Options{Alert: a, Styles: loadStyles(t)})
	out := stripStyle(p.View(120, 50))
	lines := strings.Split(out, "\n")

	// Find the description line and the next line after.
	var startIdx int
	for i, l := range lines {
		if strings.HasPrefix(l, "  description: ") {
			startIdx = i
			break
		}
	}
	require.Positive(t, startIdx)
	require.Greater(t, len(lines), startIdx+1)

	// "  description: " is 15 cols. The continuation segment
	// (the part after the embedded \n) must hang-indent by the
	// same column count so it visually nests under the value.
	cont := lines[startIdx+1]
	require.True(t, strings.HasPrefix(cont, strings.Repeat(" ", 15)),
		"line after the embedded newline must hang-indent by 15 cols, got %q", cont)
	require.Contains(t, cont, "LABELS = ",
		"the second segment of the multi-line value must appear")
}

func TestPage_WrapsLongAnnotationWithHangingIndent(t *testing.T) {
	t.Parallel()

	a := sample()
	a.Annotations = map[string]string{
		"description": "This is an alert meant to ensure that the entire alerting pipeline is functional. This alert is always firing.",
	}
	p := New(Options{Alert: a, Styles: loadStyles(t)})
	out := stripStyle(p.View(80, 50))
	lines := strings.Split(out, "\n")

	// Find the line that begins the description annotation.
	var startIdx int
	for i, l := range lines {
		if strings.HasPrefix(l, "  description: ") {
			startIdx = i
			break
		}
	}
	require.Positive(t, startIdx, "description line must appear in the render")
	// Continuation lines must be indented to align under the value
	// column ("  description: " is 15 characters of prefix).
	require.Greater(t, len(lines), startIdx+1)
	cont := lines[startIdx+1]
	require.True(t, strings.HasPrefix(cont, strings.Repeat(" ", 15)),
		"continuation line must hang-indent to %d cols, got %q", 15, cont)
}

func TestPage_ScrollsViewport(t *testing.T) {
	t.Parallel()

	a := sample()
	// Pad annotations with many short keys so the body exceeds a
	// short height and j must scroll the viewport.
	a.Annotations = map[string]string{}
	for i := range 20 {
		a.Annotations["k"+string(rune('a'+i))] = "v" + string(rune('a'+i))
	}
	p := New(Options{Alert: a, Styles: loadStyles(t)})
	// Render at a tiny height that won't show the full body.
	out := stripStyle(p.View(80, 10))
	require.NotContains(t, out, "kt: vt",
		"with a small viewport the bottom annotations must NOT appear yet")

	// G jumps to the bottom; the last keys must come into view.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	out = stripStyle(p.View(80, 10))
	require.Contains(t, out, "kt: vt",
		"after G the bottom of the body must be visible")
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

// suppressedSample builds the canonical suppressed Alert used by
// the Suppression-block tests. Optional buckets default to nil so
// callers can construct any subset of the three reason categories.
func suppressedSample(silencedBy, inhibitedBy, mutedBy []string) backend.Alert {
	a := sample()
	a.State = backend.AlertStateSuppressed
	a.SilencedBy = silencedBy
	a.InhibitedBy = inhibitedBy
	a.MutedBy = mutedBy
	return a
}

func renderSuppressed(t *testing.T, a backend.Alert, width int) string {
	t.Helper()
	p := New(Options{Alert: a, Styles: loadStyles(t), Now: func() time.Time { return fixedNow }})
	return stripStyle(p.View(width, 30))
}

func TestPage_SuppressionBlockOnlyForSuppressed(t *testing.T) {
	t.Parallel()
	// Active alert: no Suppression header, regardless of whether
	// SilencedBy/InhibitedBy/MutedBy happen to be populated.
	a := sample()
	a.SilencedBy = []string{"never-rendered"}
	out := renderSuppressed(t, a, 100)
	require.NotContains(t, out, "Suppression:",
		"non-suppressed state must NOT render the Suppression block")
}

func TestPage_SuppressionBlockSilencedByOnly(t *testing.T) {
	t.Parallel()
	out := renderSuppressed(t, suppressedSample([]string{"s1", "s2"}, nil, nil), 120)
	require.Contains(t, out, "Suppression:")
	require.Contains(t, out, "silenced by:  s1, s2")
	require.NotContains(t, out, "inhibited by:")
	require.NotContains(t, out, "muted by:")
}

func TestPage_SuppressionBlockInhibitedByOnly(t *testing.T) {
	t.Parallel()
	out := renderSuppressed(t, suppressedSample(nil, []string{"fp1"}, nil), 120)
	require.Contains(t, out, "Suppression:")
	require.Contains(t, out, "inhibited by: fp1")
	require.NotContains(t, out, "silenced by:")
	require.NotContains(t, out, "muted by:")
}

func TestPage_SuppressionBlockMutedByOnly(t *testing.T) {
	t.Parallel()
	out := renderSuppressed(t, suppressedSample(nil, nil, []string{"out-of-hours"}), 120)
	require.Contains(t, out, "Suppression:")
	require.Contains(t, out, "muted by:     out-of-hours")
	require.NotContains(t, out, "silenced by:")
	require.NotContains(t, out, "inhibited by:")
}

func TestPage_SuppressionBlockAllThreeInStableOrder(t *testing.T) {
	t.Parallel()
	out := renderSuppressed(t, suppressedSample(
		[]string{"s1"},
		[]string{"fp1"},
		[]string{"out-of-hours"},
	), 120)
	silencedAt := strings.Index(out, "silenced by:")
	inhibitedAt := strings.Index(out, "inhibited by:")
	mutedAt := strings.Index(out, "muted by:")
	require.Positive(t, silencedAt)
	require.Greater(t, inhibitedAt, silencedAt,
		"inhibited-by must follow silenced-by")
	require.Greater(t, mutedAt, inhibitedAt,
		"muted-by must follow inhibited-by")
}

func TestPage_SuppressionBlockEmptyFallback(t *testing.T) {
	t.Parallel()
	// State == suppressed but every bucket empty — defensive
	// fallback so the user sees an explanation rather than a
	// dangling header.
	a := sample()
	a.State = backend.AlertStateSuppressed
	out := renderSuppressed(t, a, 120)
	require.Contains(t, out, "Suppression:")
	require.Contains(t, out, "(no reason reported by Alertmanager)")
}

func TestPage_SuppressionBlockWrapsLongList(t *testing.T) {
	t.Parallel()
	// A wide-enough comma list at narrow width should wrap with
	// hanging indent — second line starts with the same column
	// width as the prefix (`  silenced by:  ` = 16 columns).
	long := []string{
		"silence-id-aaaaaaaaaaaaaaaaaaa",
		"silence-id-bbbbbbbbbbbbbbbbbbb",
	}
	out := renderSuppressed(t, suppressedSample(long, nil, nil), 40)
	// Find the silenced-by line and the line directly after it;
	// the continuation must start with the hanging indent.
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "silenced by:") && i+1 < len(lines) {
			next := lines[i+1]
			require.True(t, strings.HasPrefix(next, "                "),
				"continuation line must start with the 16-col hanging "+
					"indent so wrapped IDs align under the value column "+
					"(got %q)", next)
			return
		}
	}
	t.Fatal("did not find a silenced-by line followed by a continuation")
}
