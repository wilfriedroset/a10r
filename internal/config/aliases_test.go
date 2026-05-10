// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeAliases(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, AliasesFile), []byte(body), 0o600))
	return dir
}

func TestLoadAliases_RoundTrip(t *testing.T) {
	t.Parallel()

	dir := writeAliases(t, `
prod: "tenant prod"
stg: "tenant staging"
quit: "q"
`)
	got, err := LoadAliases(dir)
	require.NoError(t, err)
	require.Equal(t, AliasMap{
		"prod": "tenant prod",
		"stg":  "tenant staging",
		"quit": "q",
	}, got)
}

func TestLoadAliases_MissingFileIsNoError(t *testing.T) {
	t.Parallel()

	// Bare TempDir with no aliases.yaml — must surface an empty
	// (but non-nil) map and no error so callers can iterate without
	// branching on the missing-file case.
	got, err := LoadAliases(t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestLoadAliases_EmptyDirArgIsNoError(t *testing.T) {
	t.Parallel()

	// Defensive: a caller wiring through a flag that hasn't been
	// resolved yet shouldn't see a panic / error. Empty dir means
	// "no aliases" rather than "look in cwd".
	got, err := LoadAliases("")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestLoadAliases_EmptyFileIsNoError(t *testing.T) {
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
			dir := writeAliases(t, body)
			got, err := LoadAliases(dir)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Empty(t, got)
		})
	}
}

func TestLoadAliases_MalformedYAMLErrors(t *testing.T) {
	t.Parallel()

	// `: stray colon` is not a valid YAML mapping. The decoder
	// surfaces it; we wrap with the source path so the operator can
	// open the right file.
	dir := writeAliases(t, ":\n  -\n  -bad indent\n")
	_, err := LoadAliases(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), AliasesFile)
}

func TestLoadAliases_NonMappingErrors(t *testing.T) {
	t.Parallel()

	// A top-level sequence isn't a {short: expanded} map — strict
	// decode rejects it instead of silently producing an empty map.
	dir := writeAliases(t, "- prod\n- staging\n")
	_, err := LoadAliases(dir)
	require.Error(t, err)
}

func TestLoadAliases_RejectsEmptyValue(t *testing.T) {
	t.Parallel()

	dir := writeAliases(t, "prod: \"\"\n")
	_, err := LoadAliases(dir)
	require.ErrorIs(t, err, ErrAliasInvalid)
	require.Contains(t, err.Error(), "prod")
}

func TestLoadAliases_RejectsWhitespaceOnlyValue(t *testing.T) {
	t.Parallel()

	dir := writeAliases(t, "prod: \"   \"\n")
	_, err := LoadAliases(dir)
	require.ErrorIs(t, err, ErrAliasInvalid)
}

func TestLoadAliases_RejectsValueWithNewline(t *testing.T) {
	t.Parallel()

	// A multi-line expansion would break prompt rendering — disallow
	// it at load time so the failure mode is loud.
	dir := writeAliases(t, "prod: |\n  tenant prod\n  silences\n")
	_, err := LoadAliases(dir)
	require.ErrorIs(t, err, ErrAliasInvalid)
	require.Contains(t, err.Error(), "newlines")
}

func TestLoadAliases_RejectsKeyWithWhitespace(t *testing.T) {
	t.Parallel()

	// The cmdbar tokenises on whitespace so a key with a space could
	// never match. Surface as an error rather than silently registering
	// a dead alias.
	dir := writeAliases(t, "\"my prod\": \"tenant prod\"\n")
	_, err := LoadAliases(dir)
	require.ErrorIs(t, err, ErrAliasInvalid)
	require.Contains(t, err.Error(), "whitespace")
}

// Sanity check: the sentinel works with errors.Is across a wrap.
func TestAliasErrors_Wrap(t *testing.T) {
	t.Parallel()

	dir := writeAliases(t, "prod: \"\"\n")
	_, err := LoadAliases(dir)
	require.ErrorIs(t, err, ErrAliasInvalid)
}
