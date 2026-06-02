// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeKeys lays down a single key-overrides YAML under the
// "default" profile and returns the dir the tests pass to
// LoadKeys. Every caller targets the default profile; if a future
// test needs a named profile, take the parameter back as a variadic
// or split the helper.
func writeKeys(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, KeysDir), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, KeysDir, "default.yaml"), []byte(body), 0o600))
	return dir
}

func TestLoadKeys_RoundTrip(t *testing.T) {
	t.Parallel()

	// Bare uppercase letters are canonicalised to `Shift+<X>` at load
	// time so they reach the dispatcher in the same shape the
	// bubbletea normaliser emits at runtime — see canonicaliseKey for
	// the rationale.
	dir := writeKeys(t, `
quit: ['Q']
silence: ['S', 's2']
`)
	got, err := LoadKeys(dir, "default")
	require.NoError(t, err)
	require.Equal(t, KeyOverrides{
		"quit":    {"Shift+Q"},
		"silence": {"Shift+S", "s2"},
	}, got)
}

func TestLoadKeys_ScalarSugarEqualsSingletonList(t *testing.T) {
	t.Parallel()

	// `quit: Q` is equivalent to `quit: [Q]` per the schema sugar so
	// the simplest user file ("just give me one extra key") doesn't
	// require list syntax. Tested here to lock the equivalence so a
	// future refactor can't quietly drop it.
	dir := writeKeys(t, "quit: Q\n")
	got, err := LoadKeys(dir, "default")
	require.NoError(t, err)
	require.Equal(t, KeyOverrides{"quit": {"Shift+Q"}}, got)
}

func TestLoadKeys_MissingFileIsNoError(t *testing.T) {
	t.Parallel()

	got, err := LoadKeys(t.TempDir(), "default")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestLoadKeys_EmptyDirArgIsNoError(t *testing.T) {
	t.Parallel()

	got, err := LoadKeys("", "default")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestLoadKeys_EmptyProfileFallsBackToDefault(t *testing.T) {
	t.Parallel()

	dir := writeKeys(t, "quit: Q\n")
	got, err := LoadKeys(dir, "")
	require.NoError(t, err)
	require.Equal(t, KeyOverrides{"quit": {"Shift+Q"}}, got)
}

func TestLoadKeys_EmptyFileVariants(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"completely empty":          "",
		"whitespace only":           "\n\n  \n",
		"explicit null":             "null\n",
		"comment only":              "# nothing here\n",
		"empty mapping placeholder": "{}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := writeKeys(t, body)
			got, err := LoadKeys(dir, "default")
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Empty(t, got)
		})
	}
}

func TestLoadKeys_RejectsReservedKey(t *testing.T) {
	t.Parallel()

	// 0-9 are the C3 tenant quick-switch — refusing the bind protects
	// the muscle-memory contract documented in keybindings.md §UX.
	cases := []struct {
		name    string
		body    string
		wantTxt string // a substring of the expected error message
	}{
		{"single digit 3", "tenant-tab: ['3']\n", `attempts to bind reserved key "3"`},
		{"single digit 0", "tenant-tab: '0'\n", `attempts to bind reserved key "0"`},
		{"second of two", "quit: ['Q', '5']\n", `attempts to bind reserved key "5"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeKeys(t, tc.body)
			_, err := LoadKeys(dir, "default")
			require.ErrorIs(t, err, ErrKeyOverrideInvalid)
			require.Contains(t, err.Error(), "0-9 are reserved")
			require.Contains(t, err.Error(), tc.wantTxt)
		})
	}
}

func TestLoadKeys_RejectsSameFileConflict(t *testing.T) {
	t.Parallel()

	// `Q` bound to two different actions in the same file is a hard
	// error: the user's intent is ambiguous, and silently picking one
	// would mask a typo. Different file vs default is fine — that's
	// the whole point of overrides.
	body := "quit: ['Q']\nrefresh: ['Q']\n"
	dir := writeKeys(t, body)
	_, err := LoadKeys(dir, "default")
	require.ErrorIs(t, err, ErrKeyOverrideInvalid)
	require.Contains(t, err.Error(), `key "Shift+Q" is also bound to action "quit"`)
	// The error must point at the second (offending) occurrence so
	// the operator opens their editor at the line they need to fix.
	require.Contains(t, err.Error(), ":2:")
}

func TestLoadKeys_SameKeyTwiceUnderSameActionIsFine(t *testing.T) {
	t.Parallel()

	// Repeated key under the same action is benign (idempotent
	// override); we coalesce silently rather than treating it as a
	// conflict.
	dir := writeKeys(t, "quit: ['Q', 'Q']\n")
	got, err := LoadKeys(dir, "default")
	require.NoError(t, err)
	require.Equal(t, KeyOverrides{"quit": {"Shift+Q", "Shift+Q"}}, got)
}

func TestLoadKeys_RejectsEmptyKey(t *testing.T) {
	t.Parallel()

	dir := writeKeys(t, "quit: ['']\n")
	_, err := LoadKeys(dir, "default")
	require.ErrorIs(t, err, ErrKeyOverrideInvalid)
	require.Contains(t, err.Error(), "key must not be empty")
}

func TestLoadKeys_RejectsEmptyAction(t *testing.T) {
	t.Parallel()

	// `'': [Q]` is a syntactic possibility we want to refuse — empty
	// action names can never resolve at apply time.
	dir := writeKeys(t, "'': ['Q']\n")
	_, err := LoadKeys(dir, "default")
	require.ErrorIs(t, err, ErrKeyOverrideInvalid)
	require.Contains(t, err.Error(), "action name must not be empty")
}

func TestLoadKeys_RejectsNonMappingTopLevel(t *testing.T) {
	t.Parallel()

	dir := writeKeys(t, "- quit\n- refresh\n")
	_, err := LoadKeys(dir, "default")
	require.ErrorIs(t, err, ErrKeyOverrideInvalid)
	require.Contains(t, err.Error(), "top-level must be a mapping")
}

func TestLoadKeys_RejectsNestedMappingValue(t *testing.T) {
	t.Parallel()

	// `quit: { foo: bar }` is neither a scalar nor a sequence —
	// reject with a precise pointer to the offending line.
	dir := writeKeys(t, "quit:\n  foo: bar\n")
	_, err := LoadKeys(dir, "default")
	require.ErrorIs(t, err, ErrKeyOverrideInvalid)
	require.Contains(t, err.Error(), "value must be a string or list of strings")
}

// TestCanonicaliseKey covers the QA-driven C3 fix: the user-facing
// key spellings the operator naturally reaches for must all reach
// the dispatcher in the same title-case modifier+key form the
// bubbletea normaliser emits at runtime. Without this rewrite,
// `quit: ['Q']` would register a binding nothing ever fires —
// bubbletea v2 reports shift+q as `Shift+Q`, never the bare letter.
func TestCanonicaliseKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		// Bare uppercase rewrites to Shift+ form.
		{"Q", "Shift+Q"},
		{"G", "Shift+G"},
		// Lowercase letters and named keys pass through untouched.
		{"q", "q"},
		{"Esc", "Esc"},
		{"Enter", "Enter"},
		// Already-canonical bindings round-trip identically.
		{"Shift+Q", "Shift+Q"},
		{"Ctrl+X", "Ctrl+X"},
		{"Alt+Space", "Alt+Space"},
		// Lowercase modifier prefixes title-case.
		{"shift+q", "Shift+Q"},
		{"ctrl+x", "Ctrl+X"},
		{"alt+space", "Alt+Space"},
		// Mixed case modifier still resolves.
		{"Ctrl+shift+S", "Ctrl+Shift+S"},
		// Chord prefixes (two lowercase letters) pass through —
		// they're not single chars and not modifier+key.
		{"gg", "gg"},
		// Unknown modifier passes the whole token through untouched
		// so the dispatcher's lookup miss surfaces it verbatim.
		{"meta+q", "meta+q"},
		// Empty stays empty.
		{"", ""},
	}
	for _, tc := range cases {
		got := canonicaliseKey(tc.in)
		require.Equal(t, tc.want, got, "canonicaliseKey(%q)", tc.in)
	}
}

// TestLoadKeys_LowercaseShiftPrefixCanonicalises pins the YAML-side
// flavour: an operator writing `quit: ['shift+q']` reaches the same
// dispatcher binding as the bare `'Q'` form so the two spellings
// can't drift.
func TestLoadKeys_LowercaseShiftPrefixCanonicalises(t *testing.T) {
	t.Parallel()
	dir := writeKeys(t, "quit: ['shift+q']\n")
	got, err := LoadKeys(dir, "default")
	require.NoError(t, err)
	require.Equal(t, KeyOverrides{"quit": {"Shift+Q"}}, got)
}
