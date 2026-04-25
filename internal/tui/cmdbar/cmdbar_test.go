// SPDX-License-Identifier: Apache-2.0

package cmdbar

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"
)

// markerMsg is the test handler's output. Each handler emits a
// markerMsg keyed to its alias so tests can verify which handler
// ran without bringing tea.Program online.
type markerMsg struct {
	Alias string
	Args  []string
}

// build returns a Resolver with three aliases registered for the
// alias / prefix matching tests.
func build() *Resolver {
	r := New()
	for _, a := range []string{"alerts", "silences", "status", "tenant"} {
		alias := a
		r.Register(alias, func(args []string) tea.Cmd {
			return func() tea.Msg { return markerMsg{Alias: alias, Args: args} }
		})
	}
	return r
}

func TestResolver_ExactMatch(t *testing.T) {
	t.Parallel()

	cmd, err := build().Resolve("alerts")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, "alerts", msg.Alias)
	require.Empty(t, msg.Args)
}

func TestResolver_PrefixMatch(t *testing.T) {
	t.Parallel()

	// `sil` is a unique prefix for `silences`.
	cmd, err := build().Resolve("sil")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, "silences", msg.Alias,
		"unique prefix must resolve to the full alias")
}

func TestResolver_AmbiguousPrefix(t *testing.T) {
	t.Parallel()

	// `s` is a prefix of both `silences` and `status`.
	_, err := build().Resolve("s")
	require.ErrorIs(t, err, ErrAmbiguous)
	require.Contains(t, err.Error(), "silences")
	require.Contains(t, err.Error(), "status")
}

func TestResolver_UnknownAlias(t *testing.T) {
	t.Parallel()

	_, err := build().Resolve("nope")
	require.ErrorIs(t, err, ErrUnknown)
	require.Contains(t, err.Error(), "nope")
}

func TestResolver_EmptyInput(t *testing.T) {
	t.Parallel()

	cases := []string{"", "   ", "\t\n  "}
	for _, in := range cases {
		_, err := build().Resolve(in)
		require.ErrorIs(t, err, ErrEmpty)
	}
}

func TestResolver_LeadingTrailingWhitespace(t *testing.T) {
	t.Parallel()

	// strings.Fields tolerates leading/trailing whitespace and
	// runs of internal whitespace. Pin that behaviour so a future
	// refactor to bytes.Cut etc. doesn't silently regress.
	cmd, err := build().Resolve("   alerts   ")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, "alerts", msg.Alias)
}

func TestResolver_TabSeparatedArgs(t *testing.T) {
	t.Parallel()

	cmd, err := build().Resolve("tenant\tprod\tstaging")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, []string{"prod", "staging"}, msg.Args)
}

func TestResolver_IsCaseSensitive(t *testing.T) {
	t.Parallel()

	// Aliases match exactly. "ALERTS" must not resolve to "alerts".
	// This is intentional so a future :TENANT vs :tenant ergonomic
	// decision (left for a downstream commit) stays a design call,
	// not an accident of the resolver's matching rule.
	_, err := build().Resolve("ALERTS")
	require.ErrorIs(t, err, ErrUnknown)
}

func TestResolver_PassesArgsToHandler(t *testing.T) {
	t.Parallel()

	cmd, err := build().Resolve("tenant prod staging")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, "tenant", msg.Alias)
	require.Equal(t, []string{"prod", "staging"}, msg.Args)
}

func TestResolver_PrefixPassesArgs(t *testing.T) {
	t.Parallel()

	// `t` matches both `tenant` and (none — wait, tenant is unique
	// in this fixture). Use a prefix that's unique.
	cmd, err := build().Resolve("ten prod")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, "tenant", msg.Alias)
	require.Equal(t, []string{"prod"}, msg.Args)
}

func TestResolver_LongestMatchPrecedenceOverPrefix(t *testing.T) {
	t.Parallel()

	// If the input matches an alias exactly AND is also a prefix of
	// other aliases, the exact match wins. This pins the rule
	// against future-resolver-author confusion.
	r := New()
	r.Register("s", func(_ []string) tea.Cmd {
		return func() tea.Msg { return markerMsg{Alias: "s"} }
	})
	r.Register("status", func(_ []string) tea.Cmd {
		return func() tea.Msg { return markerMsg{Alias: "status"} }
	})

	cmd, err := r.Resolve("s")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, "s", msg.Alias,
		"exact match must beat ambiguous prefix")
}

func TestResolver_AliasesReturnsSorted(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]string{"alerts", "silences", "status", "tenant"},
		build().Aliases())
}

func TestResolver_RegisterPanicsOnEmptyAlias(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		New().Register("", func(_ []string) tea.Cmd { return nil })
	})
}

func TestResolver_RegisterPanicsOnNilHandler(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		New().Register("alerts", nil)
	})
}

// errorWraps is a sanity check that callers using errors.Is /
// errors.As against the sentinels still work after we wrapped the
// candidate list into the Ambiguous error.
func TestResolver_ErrorsWrap(t *testing.T) {
	t.Parallel()
	r := New()
	r.Register("alerts", func(_ []string) tea.Cmd { return nil })
	r.Register("alertgroups", func(_ []string) tea.Cmd { return nil })

	_, err := r.Resolve("ale")
	require.ErrorIs(t, err, ErrAmbiguous)
}
