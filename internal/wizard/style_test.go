// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The color-off styler must round-trip the exact prompt lines the
// pre-styling Prompter produced — every existing prompt_test.go
// assertion expects byte-identical output, so any drift here would
// silently break the test suite.

func TestStyler_ChoiceColorOffMatchesLegacyLayout(t *testing.T) {
	t.Parallel()

	s := newStyler(false)
	got := s.Choice("backend kind", []string{"alertmanager", "mimir"}, "alertmanager")
	require.Equal(t, "backend kind (alertmanager/mimir) [alertmanager]: ", got)
}

// Color-on assertions: the styler must wrap the chrome in bold-only
// SGR and the default value in bold + bright-blue (ANSI 12); the
// raw text content stays intact between the escapes so substring
// assertions on visible text still work.

func TestStyler_StringColorOnBoldsChromeAndColoursDefault(t *testing.T) {
	t.Parallel()

	s := newStyler(true)
	got := s.String("backend name", "prod")

	require.Contains(t, got, "backend name")
	require.Contains(t, got, "prod")
	require.Contains(t, got, "[")
	require.Contains(t, got, "]: ")
	// SGR escape present
	require.Contains(t, got, "\x1b[")
	// bold attribute (SGR 1) present somewhere
	require.True(t, strings.Contains(got, ";1m") || strings.Contains(got, "[1m"),
		"bold SGR missing in %q", got)
	// foreground colour 12 (bright blue) present
	require.Contains(t, got, "94", "bright-blue fg (SGR 94) missing in %q", got)
}

func TestStyler_StringColorOnNoDefaultStillBolds(t *testing.T) {
	t.Parallel()

	s := newStyler(true)
	got := s.String("backend name", "")

	require.Contains(t, got, "backend name")
	require.Contains(t, got, ": ")
	require.Contains(t, got, "\x1b[")
	// no default → no bright-blue (SGR 94) should appear
	require.NotContains(t, got, "94",
		"no default value must mean no bright-blue colour; got %q", got)
}

func TestStyler_ChoiceColorOnHighlightsOnlyBracketedDefault(t *testing.T) {
	t.Parallel()

	s := newStyler(true)
	got := s.Choice("backend kind", []string{"alertmanager", "mimir"}, "alertmanager")

	require.Contains(t, got, "backend kind")
	require.Contains(t, got, "alertmanager/mimir")
	require.Contains(t, got, "alertmanager")
	require.Contains(t, got, "\x1b[")
	// bright-blue must appear exactly once — only the bracketed copy
	// of the default value is highlighted, not the in-parens listing.
	require.Equal(t, 1, strings.Count(got, "94"),
		"bright-blue must appear exactly once (bracketed default only); got %q", got)
}

func TestStyler_BoolColorOnHighlightsDefaultLetter(t *testing.T) {
	t.Parallel()

	s := newStyler(true)

	yes := s.Bool("ok", true)
	require.Contains(t, yes, "ok ")
	require.Contains(t, yes, "Y")
	require.Contains(t, yes, "/n]")
	require.Contains(t, yes, "94")

	no := s.Bool("ok", false)
	require.Contains(t, no, "[y/")
	require.Contains(t, no, "N")
	require.Contains(t, no, "94")
}

func TestStyler_InvalidColorOnIsBoldRed(t *testing.T) {
	t.Parallel()

	s := newStyler(true)
	got := s.Invalid("reserved word")

	require.Contains(t, got, "  invalid: ")
	require.Contains(t, got, "reserved word")
	require.True(t, strings.HasSuffix(got, "\n"))
	// ANSI red (bright-red SGR 91) present
	require.Contains(t, got, "91", "bright-red fg (SGR 91) missing in %q", got)
}
