// SPDX-License-Identifier: Apache-2.0

package alert

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

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

func TestPage_OpensInPushTimeFormat(t *testing.T) {
	t.Parallel()

	// A page pushed *after* the user toggled `t` to absolute must
	// open already in absolute mode — without this, a detail page
	// drilled from the alerts list would briefly read 5m ago while
	// the parent showed an ISO timestamp behind it.
	p := New(Options{
		Alert:      sample(),
		Tenant:     "prod",
		Styles:     testutil.LoadStyles(t),
		Now:        func() time.Time { return fixedNow },
		TimeFormat: app.TimeFormatAbsolute,
	})
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "started:",
		"absolute mode swaps the age label to started")
	require.Contains(t, out, "2026-",
		"absolute mode renders ISO local on first View")
}

func TestPage_TimeFormatToggleSwitchesAgeLine(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Alert:  sample(),
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "5m ago")
	require.NotContains(t, out, "2026-",
		"relative mode must not surface the absolute date")

	_, _ = p.Update(app.TimeFormatChangedMsg{Format: app.TimeFormatAbsolute})
	out = testutil.StripStyle(p.View(120, 30))
	require.NotContains(t, out, "5m ago")
	require.Contains(t, out, "2026-",
		"absolute mode must surface the ISO local date prefix on the age line")
}

func TestPage_RenderAppliesYAMLKeyAndValueStyles(t *testing.T) {
	t.Parallel()

	styles := testutil.LoadStyles(t)
	p := New(Options{
		Alert:  sample(),
		Tenant: "prod",
		Styles: styles,
		Now:    func() time.Time { return fixedNow },
	})
	raw := p.View(120, 30)

	// Sanity: stripped output keeps the underlying YAML-shaped lines.
	require.Contains(t, testutil.StripStyle(raw), "alertname:")
	require.Contains(t, testutil.StripStyle(raw), "severity:")

	// The "Labels:" and "Annotations:" section headers — and every
	// "key: value" pair underneath — must paint the foreground via
	// the skin's YAML.Key role. Easiest portable proof: the rendered
	// substring for a known key ("alertname") matches what
	// styles.YAML.Key.Render produces when called in isolation.
	require.Contains(t, raw, styles.YAML.Key.Render("alertname"),
		"alert detail must colour `alertname` with the skin's YAML.Key foreground")
	require.Contains(t, raw, styles.YAML.Key.Render("Labels"),
		"section headers must paint the foreground via YAML.Key")
}

func TestPage_RenderShowsAllSections(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Alert:  sample(),
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	out := testutil.StripStyle(p.View(120, 30))
	for _, want := range []string{
		"HighCPU", "active", "critical", "abc123",
		"5m ago", "host-1", "CPU is hot",
		"https://example.test/graph?abc", "prod",
	} {
		require.Contains(t, out, want, "missing %q in render", want)
	}
}

func TestPage_HeaderContentIsEmpty(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Alert:  sample(),
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
	})
	require.Empty(t, p.HeaderContent(),
		"title shows <tenant>/<alertname> and the summary surfaces state + "+
			"tenant on their own lines — a header subtitle would duplicate both")
}

func TestPage_CopyFingerprintSuccess(t *testing.T) {
	t.Parallel()

	clip := &fakeClipboard{}
	p := New(Options{
		Alert:     sample(),
		Styles:    testutil.LoadStyles(t),
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

	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t)})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "clipboard")
}

func TestPage_CopyFingerprintErrorFlashesError(t *testing.T) {
	t.Parallel()

	clip := &fakeClipboard{wantErr: errors.New("display server unreachable")}
	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t), Clipboard: clip})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "display server unreachable")
}

func TestPage_OpenURLSuccess(t *testing.T) {
	t.Parallel()

	br := &fakeBrowser{}
	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t), Browser: br})
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
	p := New(Options{Alert: a, Styles: testutil.LoadStyles(t), Browser: br})
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
	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t), Browser: br})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "no display server")
}

func TestPage_OpenURLWithoutBrowserFlashesWarn(t *testing.T) {
	t.Parallel()

	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t)})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
}

func TestPage_SilenceWithoutClientsFlashesHint(t *testing.T) {
	t.Parallel()

	p := New(Options{Alert: sample(), Tenant: "prod", Styles: testutil.LoadStyles(t)})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no writeable backend",
		"`s` with no clients must explain rather than push a broken form")
}

func TestPage_SilencePushesFormWhenClientsAreConfigured(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Alert:   sample(),
		Tenant:  "prod",
		Styles:  testutil.LoadStyles(t),
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
		Creator: "wilfried",
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd, "`s` must produce a Cmd that pushes the form")
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "`s` with clients must push the form, not flash")
}

func TestPage_SilenceTenantNotInClientsFlashesHint(t *testing.T) {
	t.Parallel()
	// User drilled in from a tenant the silenceClients map doesn't
	// cover (e.g. the tenant config went away mid-session). Flash
	// rather than crash.
	p := New(Options{
		Alert:   sample(),
		Tenant:  "ghost",
		Styles:  testutil.LoadStyles(t),
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no writeable backend")
}

func TestPage_SilenceFormSubmittedFlashesSuccess(t *testing.T) {
	t.Parallel()
	p := New(Options{Alert: sample(), Tenant: "prod", Styles: testutil.LoadStyles(t)})
	_, cmd := p.Update(silenceform.SubmittedMsg{ID: "sil-99"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "silence created: sil-99")
}

// fakeSilenceClient satisfies silenceform.Client so the `s`
// push test can construct a non-nil Clients map. The detail
// page never actually invokes its methods in tests.
type fakeSilenceClient struct{}

func (*fakeSilenceClient) CreateSilence(_ context.Context, _ backend.SilenceSpec) (string, error) {
	return "fake-silence-id", nil
}

func (*fakeSilenceClient) UpdateSilence(_ context.Context, _ string, _ backend.SilenceSpec) error {
	return nil
}

func TestPage_BindingsHaveCopyOpenSilence(t *testing.T) {
	t.Parallel()

	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Alert: a, Styles: testutil.LoadStyles(t)})

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
	p := New(Options{Alert: a, Styles: testutil.LoadStyles(t)})
	out := testutil.StripStyle(p.View(120, 50))
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
	p := New(Options{Alert: a, Styles: testutil.LoadStyles(t)})
	out := testutil.StripStyle(p.View(80, 50))
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
	p := New(Options{Alert: a, Styles: testutil.LoadStyles(t)})
	// Render at a tiny height that won't show the full body.
	out := testutil.StripStyle(p.View(80, 10))
	require.NotContains(t, out, "kt: vt",
		"with a small viewport the bottom annotations must NOT appear yet")

	// G jumps to the bottom; the last keys must come into view.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	out = testutil.StripStyle(p.View(80, 10))
	require.Contains(t, out, "kt: vt",
		"after G the bottom of the body must be visible")
}

func TestPage_FullPageMotionsScrollViewport(t *testing.T) {
	t.Parallel()
	// Cold-start: no View call yet → 20-line fallback.
	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t)})
	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll, "cold-start Ctrl+F falls back to 20 lines")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll, "Ctrl+B mirrors Ctrl+F")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll, "Ctrl+B clamps at 0")
}

func TestPage_ViewportAwareScrollSteps(t *testing.T) {
	t.Parallel()
	// Pad annotations so the body has plenty of lines for the test
	// to walk a viewport-aware step without immediately clamping.
	a := sample()
	a.Annotations = map[string]string{}
	for i := range 200 {
		a.Annotations[fmt.Sprintf("k%03d", i)] = fmt.Sprintf("v%03d", i)
	}
	p := New(Options{Alert: a, Styles: testutil.LoadStyles(t)})
	_ = p.View(120, 40) // 40-line viewport — half=20, full=body-2=38

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll, "Ctrl+D walks half the rendered body (40 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 58, p.scroll, "Ctrl+F walks body-2 (20 + 38)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll)
}

func TestPage_RenderHandlesEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	a := backend.Alert{
		Labels: map[string]string{"alertname": "Bare"},
		State:  backend.AlertStateActive,
	}
	p := New(Options{Alert: a, Styles: testutil.LoadStyles(t)})
	out := testutil.StripStyle(p.View(80, 20))
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
	p := New(Options{Alert: a, Styles: testutil.LoadStyles(t), Now: func() time.Time { return fixedNow }})
	return testutil.StripStyle(p.View(width, 30))
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
	// Cache miss path (no silences ingested) — the section header
	// renders, IDs appear one per row under it with the
	// not-in-snapshot marker. Enriched-row coverage lives in its
	// own test that feeds a poll.DataMsg.
	out := renderSuppressed(t, suppressedSample([]string{"s1", "s2"}, nil, nil), 120)
	require.Contains(t, out, "Suppression:")
	require.Contains(t, out, "silenced by:")
	require.Contains(t, out, "    s1  (silence not in snapshot)")
	require.Contains(t, out, "    s2  (silence not in snapshot)")
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

// silenceDataMsg builds a poll.DataMsg the alert page accepts as a
// silences-resource snapshot for the given tenant. Used by tests
// that need the suppression block to render enriched rows.
func silenceDataMsg(tenant string, sils []backend.Silence) poll.DataMsg {
	return poll.DataMsg{
		ResourceLabel: "silences",
		Tenant:        tenant,
		Resource:      sils,
	}
}

func TestPage_SilencedByEnrichedFromCache(t *testing.T) {
	t.Parallel()
	// The polled silences snapshot for p.tenant must enrich the
	// silenced-by row with expiry / by / comment. Comment is the
	// last column so a quick scan lands on the human reason.
	a := suppressedSample([]string{"sil-1"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{{
		ID:        "sil-1",
		EndsAt:    fixedNow.Add(2*time.Hour + 13*time.Minute),
		CreatedBy: "alice",
		Comment:   "investigating spike",
		State:     backend.SilenceStateActive,
	}}))
	out := testutil.StripStyle(p.View(120, 30))

	require.Contains(t, out, "    sil-1  expires in 2h13m  by alice  — investigating spike",
		"enriched row must inline id, expiry, creator, and comment with — separator")
	require.NotContains(t, out, "(silence not in snapshot)",
		"cache hit must not surface the degraded marker")
}

func TestPage_SilencedByOnlyTenantTrustedFromCache(t *testing.T) {
	t.Parallel()
	// A silences snapshot for a *different* tenant must NOT enrich
	// the row — silenced-by IDs are not cross-tenant, and trusting
	// a stranger tenant's snapshot would surface incorrect details.
	a := suppressedSample([]string{"sil-1"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	// Same ID, but ingested under a different tenant tag.
	_, _ = p.Update(silenceDataMsg("staging", []backend.Silence{{
		ID:        "sil-1",
		EndsAt:    fixedNow.Add(time.Hour),
		CreatedBy: "mallory",
		Comment:   "wrong-tenant payload",
	}}))
	out := testutil.StripStyle(p.View(120, 30))

	require.Contains(t, out, "(silence not in snapshot)",
		"the page must drop foreign-tenant payloads — they could attribute the wrong reason to a silence")
	require.NotContains(t, out, "wrong-tenant payload")
}

func TestPage_SilencedByDegradedRowOnCacheMiss(t *testing.T) {
	t.Parallel()
	a := suppressedSample([]string{"missing-id"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	// Ingest a snapshot that doesn't contain the alert's silenced-by
	// ID — represents a cold start, recently-expired silence still
	// referenced by the alert, or backend asymmetry.
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{{ID: "other-id"}}))
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "    missing-id  (silence not in snapshot)")
}

func TestPage_SilencedByCommentClippedNoWrap(t *testing.T) {
	t.Parallel()
	// At a narrow width a long comment must clip with "…" rather
	// than wrap onto a second line — wrapping would push the next
	// silence row out of column alignment, exactly the UI mess the
	// design explicitly avoids.
	a := suppressedSample([]string{"sil-long"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{{
		ID:        "sil-long",
		EndsAt:    fixedNow.Add(time.Hour),
		CreatedBy: "alice",
		Comment:   strings.Repeat("X", 500),
	}}))
	// Render with a generous height so we always see the row plus
	// the line after it; without this guard a small viewport could
	// place the silenced-by row at the visible bottom and there'd
	// be no "next line" to inspect.
	out := testutil.StripStyle(p.View(80, 200))
	lines := strings.Split(out, "\n")

	var rowIdx int
	for i, l := range lines {
		if strings.Contains(l, "sil-long") {
			rowIdx = i
			break
		}
	}
	require.Positive(t, rowIdx, "silenced-by row must appear in render")
	row := lines[rowIdx]
	require.Contains(t, row, "…",
		"clipped comment must end with the ellipsis marker")
	require.LessOrEqual(t, lipgloss.Width(row), 80,
		"clipped row must fit within the rendered width — wrapping the comment is the bug we're guarding against")
	// No subsequent line may carry the clipped comment payload.
	// Walking every following line catches a hang-wrap regardless
	// of whether the row happens to be the last one in the body.
	for i := rowIdx + 1; i < len(lines); i++ {
		require.NotContains(t, lines[i], "X",
			"clipped comment must not bleed into a later line, got %q", lines[i])
	}
}

func TestPage_SilencedByCommentTruncatedAtFirstNewline(t *testing.T) {
	t.Parallel()
	a := suppressedSample([]string{"sil-multi"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{{
		ID:        "sil-multi",
		EndsAt:    fixedNow.Add(time.Hour),
		CreatedBy: "alice",
		Comment:   "headline\nfollow-up detail nobody needs in the row",
	}}))
	out := testutil.StripStyle(p.View(160, 30))
	require.Contains(t, out, "— headline…",
		"first line must surface with ellipsis indicating hidden continuation")
	require.NotContains(t, out, "follow-up detail",
		"second-line content must NOT appear in the row")
}

func TestPage_SilencedByExpiryFlipsLabelInAbsoluteMode(t *testing.T) {
	t.Parallel()
	a := suppressedSample([]string{"sil-1"}, nil, nil)
	p := New(Options{
		Alert:      a,
		Tenant:     "prod",
		Styles:     testutil.LoadStyles(t),
		Now:        func() time.Time { return fixedNow },
		TimeFormat: app.TimeFormatAbsolute,
	})
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{{
		ID:        "sil-1",
		EndsAt:    fixedNow.Add(time.Hour),
		CreatedBy: "alice",
		Comment:   "x",
	}}))
	out := testutil.StripStyle(p.View(160, 30))
	require.Contains(t, out, "ends ",
		"absolute mode must label the column 'ends '")
	require.NotContains(t, out, "expires in",
		"absolute mode must drop the relative-mode label")
}

func TestPage_PollResourcesIncludesSilences(t *testing.T) {
	t.Parallel()
	// The page must opt in to the silences feed so the App's cache
	// replay hydrates a freshly-pushed detail view immediately.
	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t)})
	require.Equal(t, []string{"silences"}, p.PollResources())
}

func TestPage_OpenSilenceFlashesWhenNoSilencedBy(t *testing.T) {
	t.Parallel()
	// Active alert with no silenced-by IDs: `S` is a soft no-op.
	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t)})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "no silences")
}

func TestPage_OpenSilenceN1PushesDetail(t *testing.T) {
	t.Parallel()
	a := suppressedSample([]string{"sil-1"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{{
		ID:        "sil-1",
		EndsAt:    fixedNow.Add(time.Hour),
		CreatedBy: "alice",
	}}))
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.NotNil(t, cmd)
	// pushPageMsg is unexported in the app package, so we assert on
	// the type *name* rather than direct match. Stronger than a
	// "not flash" check: a regression that swapped the N=1 / N>1
	// branches would emit openModalMsg here and slip past the
	// looser assertion.
	require.Contains(t, fmt.Sprintf("%T", cmd()), "pushPageMsg",
		"single silence must push silence detail, not open a modal or flash")
}

func TestPage_OpenSilenceCacheMissFlashesInfo(t *testing.T) {
	t.Parallel()
	a := suppressedSample([]string{"missing-id"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	// No DataMsg ingested — cache miss.
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "missing-id")
	require.Contains(t, msg.Text, ":silences",
		"hint must point the user at the silences page so the affordance reads consistently with the rendered degraded row")
}

func TestPage_OpenSilenceN2OpensModal(t *testing.T) {
	t.Parallel()
	// Two silenced-by entries: `S` opens the disambiguation modal
	// (app.OpenModal emits an openModalMsg the App handles).
	// openModalMsg is unexported, so we assert on the type name.
	// Pinning the exact emitter type catches a regression that
	// swaps the N=1 / N>1 branches — the looser "not flash" check
	// would have happily accepted a misrouted pushPageMsg.
	a := suppressedSample([]string{"sil-1", "sil-2"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{
		{ID: "sil-1", EndsAt: fixedNow.Add(time.Hour), CreatedBy: "alice"},
		{ID: "sil-2", EndsAt: fixedNow.Add(2 * time.Hour), CreatedBy: "bob"},
	}))
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.NotNil(t, cmd)
	require.Contains(t, fmt.Sprintf("%T", cmd()), "openModalMsg",
		"N>1 must open the disambiguation modal, not push or flash")
}

func TestPage_SilencedByNarrowWidthDropsEmDashSeparator(t *testing.T) {
	t.Parallel()
	// At a width too tight to fit any comment after the prefix, the
	// row must drop the "  — " separator entirely rather than render
	// "  — " followed by nothing — a dangling em-dash reads as a
	// rendering bug.
	a := suppressedSample([]string{"sil-1"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{{
		ID:        "sil-1",
		EndsAt:    fixedNow.Add(time.Hour),
		CreatedBy: "alice",
		Comment:   "a comment that does not fit at all",
	}}))
	// 40 cols is wide enough for the prefix ("    sil-1  expires in 1h
	// by alice") but not for the separator + meaningful comment.
	out := testutil.StripStyle(p.View(40, 30))
	require.NotRegexp(t, `—\s*$`, out,
		"no row may end in a dangling em-dash")
	require.NotContains(t, out, "—  \n",
		"no row may render the em-dash separator with empty content")
}

func TestPage_SilencedByDedupesDuplicateIDs(t *testing.T) {
	t.Parallel()
	// A non-conforming upstream that emits the same silence ID twice
	// in SilencedBy must not produce two visually-identical picker
	// rows that both drill to the same silence — confusing UX. The
	// page de-duplicates at the boundary, so two-of-the-same-id
	// degrades to the single-silence direct-push path.
	a := suppressedSample([]string{"sil-1", "sil-1"}, nil, nil)
	p := New(Options{
		Alert:  a,
		Tenant: "prod",
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
	_, _ = p.Update(silenceDataMsg("prod", []backend.Silence{{
		ID:        "sil-1",
		EndsAt:    fixedNow.Add(time.Hour),
		CreatedBy: "alice",
		Comment:   "x",
	}}))
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.NotNil(t, cmd)
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash,
		"duplicate IDs collapsing to one must follow the single-silence direct-push path, not flash")
}

func TestClipComment_NeverExceedsBudget(t *testing.T) {
	t.Parallel()
	// Boundary table: every output must satisfy lipgloss.Width(out)
	// ≤ budget. Multiline at the budget boundary used to overflow
	// by one column (the "…" was appended without making room for
	// it); this test pins the corrected contract.
	cases := []struct {
		name    string
		s       string
		budget  int
		wantMax int
	}{
		{"single-line short fits", "ok", 10, 10},
		{"single-line at budget", "abcde", 5, 5},
		{"single-line over budget cuts", "abcdefgh", 5, 5},
		{"multiline short adds ellipsis", "ok\nmore", 10, 10},
		{"multiline at budget cuts to leave room for ellipsis", "abcde\nmore", 5, 5},
		{"multiline over budget cuts", "abcdefgh\nmore", 5, 5},
		{"budget 1 returns ellipsis", "abc", 1, 1},
		{"budget 0 returns empty", "abc", 0, 0},
		{"budget negative returns empty", "abc", -3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := clipComment(tc.s, tc.budget)
			require.LessOrEqual(t, lipgloss.Width(got), tc.wantMax,
				"clipComment(%q, %d) returned %q (width %d) — must not exceed budget",
				tc.s, tc.budget, got, lipgloss.Width(got))
		})
	}
}

func TestPage_BindingsIncludeOpenSilence(t *testing.T) {
	t.Parallel()
	// Capital `S` is a separate binding from lower-case `s`
	// (silence form). Both must surface in `?` help so the user can
	// discover the read-only navigation alongside the write one.
	p := New(Options{Alert: sample(), Styles: testutil.LoadStyles(t)})
	keys := map[string]bool{}
	for _, b := range p.Bindings() {
		keys[b.Key] = true
	}
	require.True(t, keys["S"], "missing capital-S binding for open-silence drilldown")
}

func TestFormatRemaining(t *testing.T) {
	t.Parallel()
	now := fixedNow
	cases := []struct {
		name string
		when time.Duration
		want string
	}{
		{"past collapses to expired", -time.Hour, "expired"},
		{"zero is expired", 0, "expired"},
		{"sub-minute renders seconds", 30 * time.Second, "30s"},
		{"sub-hour renders minutes", 45 * time.Minute, "45m"},
		{"hours and minutes", 2*time.Hour + 13*time.Minute, "2h13m"},
		{"whole hours drop the m suffix", 3 * time.Hour, "3h"},
		{"days swallow hours and minutes", 49 * time.Hour, "2d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatRemaining(now, now.Add(tc.when))
			require.Equal(t, tc.want, got)
		})
	}
}
