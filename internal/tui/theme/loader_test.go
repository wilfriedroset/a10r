// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"bytes"
	"image/color"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// isUnsetColor returns true if the lipgloss colour is in lipgloss's
// documented "no value set" state. lipgloss uses `NoColor{}` for
// this; both never-called and explicitly-NoColor produce the same
// observable rendering (no SGR for the slot). Used to assert either
// foreground or background is unset on a compiled style.
func isUnsetColor(c color.Color) bool {
	_, ok := c.(lipgloss.NoColor)
	return c == nil || ok
}

// bundledNames lists the eight catppuccin variants we ship inside
// the binary. Test-local because the production code no longer
// exposes a BundledNames() helper — `make skins-sync` is the
// system-of-record for what's embedded.
var bundledNames = []string{
	"catppuccin-frappe", "catppuccin-frappe-transparent",
	"catppuccin-latte", "catppuccin-latte-transparent",
	"catppuccin-macchiato", "catppuccin-macchiato-transparent",
	"catppuccin-mocha", "catppuccin-mocha-transparent",
}

func TestLoad_EachBundledSkinCompiles(t *testing.T) {
	t.Parallel()

	for _, name := range bundledNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles, err := (&Loader{}).Load(name)
			require.NoError(t, err)
			require.NotNil(t, styles)

			// Spot-check one role per major group so a rename or
			// missing field anywhere in compile() fails this test
			// rather than silently leaving a zero-value Style.
			//
			// Body fg is mandatory and always set (even on the
			// transparent skins it's a real colour — only `bg` is
			// `default` there).
			require.NotEmpty(t, styles.Body.Default.GetForeground())
			require.NotEmpty(t, styles.Header.Accent.GetForeground())
			require.NotEmpty(t, styles.Table.Cursor.GetBackground())
			require.NotEmpty(t, styles.Severity.Critical.GetForeground())
			require.NotEmpty(t, styles.SilenceState.Active.GetForeground())
			require.NotEmpty(t, styles.Flash.Error.GetForeground())
			require.NotEmpty(t, styles.YAML.Key.GetForeground())
			require.NotEmpty(t, styles.Crumbs.Active.GetForeground())
			require.NotEmpty(t, styles.Hint.HelpKey.GetForeground())
			require.NotEmpty(t, styles.Modal.Border.GetForeground())
		})
	}
}

func TestLoad_TransparentVariantsKeepBodyBgUnset(t *testing.T) {
	t.Parallel()

	// The four `-transparent` variants exist precisely so the
	// terminal-native bg shows through. If a refactor ever wires
	// `default` to a real colour, this test catches it.
	transparent := []string{
		"catppuccin-frappe-transparent",
		"catppuccin-latte-transparent",
		"catppuccin-macchiato-transparent",
		"catppuccin-mocha-transparent",
	}
	for _, name := range transparent {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			styles, err := (&Loader{}).Load(name)
			require.NoError(t, err)
			require.True(t, isUnsetColor(styles.Body.Default.GetBackground()),
				"body.bg must be unset (terminal-default) on -transparent variant")
		})
	}
}

func TestLoad_EmptyNameUsesDefault(t *testing.T) {
	t.Parallel()

	styles, err := (&Loader{}).Load("")
	require.NoError(t, err)
	require.NotNil(t, styles)
}

func TestLoad_UnknownNameFallsBackToDefault(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	styles, err := (&Loader{Logger: logger}).Load("does-not-exist")
	require.NoError(t, err)
	require.NotNil(t, styles)

	out := buf.String()
	require.Contains(t, out, "unknown skin")
	require.Contains(t, out, `requested=does-not-exist`)
	require.Contains(t, out, `default=catppuccin-mocha`)
}

// TestLoad_RejectsPathTraversalNames pins the path-traversal
// guard: theme.name flows into filepath.Join(UserDir,
// name+".yaml"), so a `..` segment would escape the skins
// directory and let a hostile config read arbitrary files. Loader
// rejects names outside the allowed alphabet by treating them as
// unknown — falling back to the bundled default with a warning
// rather than the malicious candidate path.
func TestLoad_RejectsPathTraversalNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	cases := []string{
		"../../etc/passwd",
		"foo/bar",
		"foo bar",
		"foo$bar",
	}
	for _, name := range cases {
		styles, err := (&Loader{UserDir: dir, Logger: logger}).Load(name)
		require.NoError(t, err, "must fall back rather than error on %q", name)
		require.NotNil(t, styles)
	}
	out := buf.String()
	require.Contains(t, out, "unknown skin",
		"path-traversal names must be treated as unknown so the loader never resolves them")
}

func TestLoad_UserSkinShadowsBundledLogsWarning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw, err := readBundled("catppuccin-mocha")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "catppuccin-mocha.yaml"), raw, 0o600))

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	styles, err := (&Loader{UserDir: dir, Logger: logger}).Load("catppuccin-mocha")
	require.NoError(t, err)
	require.NotNil(t, styles)

	require.Contains(t, buf.String(), "user skin shadows bundled skin",
		"shadow warning must fire when user file matches a bundled name")
}

func TestLoad_RejectsMalformedYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.yaml"),
		[]byte("k9s: [unclosed"), 0o600))

	_, err := (&Loader{UserDir: dir}).Load("broken")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSkin)
}

func TestLoad_RejectsMissingBodyFg(t *testing.T) {
	t.Parallel()

	// A skin missing `body.fgColor` is unrenderable: we have no
	// safe floor to fall back to. Must surface as ErrInvalidSkin.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nofg.yaml"), []byte(`
k9s:
  body:
    bgColor: "#000000"
`), 0o600))

	_, err := (&Loader{UserDir: dir}).Load("nofg")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSkin)
	require.Contains(t, err.Error(), "body.fgColor")
}

func TestLoad_RejectsBadColorValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(`
k9s:
  body:
    fgColor: "not-a-color"
    bgColor: "#000000"
`), 0o600))

	_, err := (&Loader{UserDir: dir}).Load("bad")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSkin)
	require.Contains(t, err.Error(), "unknown color")
}

func TestLoad_FallsBackToStockStatus(t *testing.T) {
	t.Parallel()

	// Mirror the `transparent.yaml` / `vercel.yaml` shape: a skin
	// that ships only body.fg/bg and relies on k9s stock for the
	// frame.status colors. Must load cleanly and produce non-empty
	// severity colors derived from stockStatus.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "minimal.yaml"), []byte(`
k9s:
  body:
    fgColor: "#ffffff"
    bgColor: "#000000"
`), 0o600))

	styles, err := (&Loader{UserDir: dir}).Load("minimal")
	require.NoError(t, err)
	require.NotNil(t, styles)

	// stockStatus.ErrorColor is `orangered` = #FF4500. The
	// severity.critical role binds to errorColor, so the compiled
	// foreground must equal that exact RGB.
	wantR, wantG, wantB := uint8(0xFF), uint8(0x45), uint8(0x00)
	got := styles.Severity.Critical.GetForeground()
	require.NotNil(t, got)
	r, g, b := colorRGBA(got)
	require.Equal(t, wantR, r, "severity.critical R")
	require.Equal(t, wantG, g, "severity.critical G")
	require.Equal(t, wantB, b, "severity.critical B")
}

func TestLoad_DefaultKeywordOnBgLeavesBackgroundUnset(t *testing.T) {
	t.Parallel()

	// `bgColor: default` is the load-bearing primitive behind every
	// `-transparent` variant. If lipgloss receives a real Color for
	// it, the terminal-native bg gets overwritten — exactly the
	// regression the user explicitly asked us to avoid.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tpass.yaml"), []byte(`
k9s:
  body:
    fgColor: "#ffffff"
    bgColor: default
`), 0o600))

	styles, err := (&Loader{UserDir: dir}).Load("tpass")
	require.NoError(t, err)
	require.True(t, isUnsetColor(styles.Body.Default.GetBackground()),
		"`bgColor: default` must produce an unset Background()")
}

func TestLoad_BadCascadeFieldNamesItsRole(t *testing.T) {
	t.Parallel()

	// A malformed color in a non-mandatory cascade field must
	// surface with the role name in the error so the user can
	// pinpoint the broken line. We exercise one cascade per major
	// compile* function so a future refactor that drops the role
	// label fails this test rather than degrading the error UX.
	tests := []struct {
		role string
		yaml string
	}{
		{
			role: "header.fg",
			yaml: `
k9s:
  body: { fgColor: "#ffffff", bgColor: "#000000" }
  frame:
    title: { fgColor: "borked" }
`,
		},
		{
			// frame.border.fgColor feeds both Frame.Border (page
			// frame border) and Modal.Border. Frame.Border is
			// compiled first, so a malformed value surfaces under
			// that role name — the test pins both the field and
			// the role label.
			role: "frame.border",
			yaml: `
k9s:
  body: { fgColor: "#ffffff", bgColor: "#000000" }
  frame:
    border: { fgColor: "borked" }
`,
		},
		{
			role: "yaml.key",
			yaml: `
k9s:
  body: { fgColor: "#ffffff", bgColor: "#000000" }
  views:
    yaml: { keyColor: "borked" }
`,
		},
		{
			role: "table.marked",
			yaml: `
k9s:
  body: { fgColor: "#ffffff", bgColor: "#000000" }
  views:
    table: { markColor: "borked" }
`,
		},
		{
			role: "crumbs.active",
			yaml: `
k9s:
  body: { fgColor: "#ffffff", bgColor: "#000000" }
  frame:
    crumbs: { activeColor: "borked" }
`,
		},
		{
			role: "hint.help_key",
			yaml: `
k9s:
  body: { fgColor: "#ffffff", bgColor: "#000000" }
  frame:
    menu: { numKeyColor: "borked" }
`,
		},
		{
			role: "prompt.suggestion",
			yaml: `
k9s:
  body: { fgColor: "#ffffff", bgColor: "#000000" }
  prompt: { suggestColor: "borked" }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, "broken.yaml"),
				[]byte(tt.yaml), 0o600,
			))
			_, err := (&Loader{UserDir: dir}).Load("broken")
			require.Error(t, err)
			require.ErrorIs(t, err, ErrInvalidSkin)
			require.Contains(t, err.Error(), tt.role,
				"error must name the role with the bad color")
		})
	}
}

func TestLoad_CatppuccinMochaCursorMatchesUpstream(t *testing.T) {
	t.Parallel()

	// Pin the cursor (active row) RGB values for catppuccin-mocha
	// against the upstream catppuccin/k9s file (commit
	// fdbec82284744a1fc2eb3e2d24cb92ef87ffb8b4):
	//
	//   views.table.cursorFgColor: '#313244'  (surface0)
	//   views.table.cursorBgColor: '#45475a'  (surface1)
	//
	// If a future refactor accidentally maps cursor to body fields
	// or to the title.bg, this test fails — `bg should match k9s`
	// is otherwise hard to debug with eyes alone.
	styles, err := (&Loader{}).Load("catppuccin-mocha")
	require.NoError(t, err)

	bg := styles.Table.Cursor.GetBackground()
	require.NotNil(t, bg)
	r, g, b := colorRGBA(bg)
	require.Equal(t, uint8(0x45), r, "cursor.bg.R")
	require.Equal(t, uint8(0x47), g, "cursor.bg.G")
	require.Equal(t, uint8(0x5A), b, "cursor.bg.B")

	fg := styles.Table.Cursor.GetForeground()
	require.NotNil(t, fg)
	r, g, b = colorRGBA(fg)
	require.Equal(t, uint8(0x31), r, "cursor.fg.R")
	require.Equal(t, uint8(0x32), g, "cursor.fg.G")
	require.Equal(t, uint8(0x44), b, "cursor.fg.B")
}

// colorRGBA is a test-only adapter: GetForeground() returns
// color.Color whose RGBA() is 16-bit-per-channel. Squash to 8-bit
// so equality assertions read in hex. Alpha is dropped because
// terminals don't render it.
func colorRGBA(c color.Color) (r, g, b uint8) {
	r16, g16, b16, _ := c.RGBA()
	return uint8(r16 >> 8), uint8(g16 >> 8), uint8(b16 >> 8)
}
