// SPDX-License-Identifier: Apache-2.0

package yamlstyle

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func TestLine_CommentPassesThroughUnstyled(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	const line = "# resolved at: 2026-05-01"
	got := Line(line, styles)
	require.Equal(t, line, got,
		"comment-only lines must pass through unstyled — the text after `#` "+
			"is human prose, not a yaml key/value pair")
}

func TestLine_NoColonPassesThrough(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	const line = "  - matchers"
	require.Equal(t, line, Line(line, styles))
}

func TestLine_KeyValueIsStyled(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	out := Line("comment: scheduled maintenance", styles)
	// Must keep the underlying text intact so :%s-style scanning works.
	require.Equal(t, "comment: scheduled maintenance", testutil.StripStyle(out))
	// And must apply the YAML.Key / Value styles — easiest portable
	// check is that the rendered output is *different* from the raw
	// line (i.e. SGR escapes were emitted).
	require.NotEqual(t, "comment: scheduled maintenance", out,
		"a non-empty key/value must receive SGR styling")
}

func TestLine_KeyOnlyStillStylesKeyAndPunct(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	out := Line("matchers:", styles)
	require.Equal(t, "matchers:", testutil.StripStyle(out))
	require.NotEqual(t, "matchers:", out)
}

func TestLine_ListElementKeyValueStyled(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// "- name: foo" — leading "- " is treated as part of the indent
	// so the key (`name`) still gets the YAML.Key role.
	out := Line("- name: foo", styles)
	require.Equal(t, "- name: foo", testutil.StripStyle(out))
	require.NotEqual(t, "- name: foo", out)
}

func TestLine_PrometheusAnnotationContinuationPassesThrough(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// kvLines splits multi-line annotation values on \n and renders
	// each segment with the hanging indent. A continuation like
	// "      LABELS = map[__name__:up]" has a `:` purely from the
	// matcher map literal — colouring "LABELS = map[__name__" as a
	// YAML key would mis-tint half the line. Must pass through.
	const line = "      LABELS = map[__name__:up cluster:foo]"
	require.Equal(t, line, Line(line, styles))
}

func TestLine_MultiWordKeyStillStyled(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// "Generator URL" / "silenced by" are not strict YAML idents
	// but they are *labels* the alert detail page builds. They
	// must still receive the YAML.Key role.
	out := Line("Generator URL: https://example.test/g?q=1", styles)
	require.Equal(t, "Generator URL: https://example.test/g?q=1", testutil.StripStyle(out))
	require.NotEqual(t, "Generator URL: https://example.test/g?q=1", out,
		"multi-word labels with only letters and spaces must keep styling")
}

func TestBody_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	require.Empty(t, Body("", styles))
}

func TestBody_PreservesLineCount(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	in := "id: abc\ncomment: hi\nmatchers:\n  - name: alertname\n    value: HighCPU\n"
	out := Body(in, styles)
	require.Equal(t, in, testutil.StripStyle(out))
}
