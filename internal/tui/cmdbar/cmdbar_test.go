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

func TestResolver_Suggest(t *testing.T) {
	t.Parallel()

	// Fixture mirrors the registered-alias shape in cmd/tui.go:
	// long forms alongside short abbreviations so the alphabetical-
	// first policy is exercised on prefixes that match more than one
	// alias.
	r := New()
	for _, a := range []string{
		"alerts", "gr", "groups", "q", "rec", "receivers",
		"sil", "silences", "status", "tenant", "tenants",
	} {
		r.Register(a, func(_ []string) tea.Cmd { return nil })
	}

	cases := []struct {
		name   string
		prefix string
		want   string
	}{
		{"empty input returns no ghost", "", ""},
		{"no match returns no ghost", "xyz", ""},

		// Single-match prefixes return the full alias so the caller
		// can show the trailing chars as ghost text.
		{"single match short prefix", "a", "alerts"},
		{"single match longer prefix", "silen", "silences"},
		{"single match prefix of receivers", "rece", "receivers"},

		// Multi-match prefixes return the alphabetically-first match.
		{"multi match s returns sil", "s", "sil"},
		{"multi match si returns sil", "si", "sil"},
		{"multi match g returns gr", "g", "gr"},
		{"multi match r returns rec", "r", "rec"},

		// Exact match — even when a longer alias shares the prefix,
		// no ghost is shown so users who deliberately typed the
		// short form aren't auto-completed past it.
		{"exact match short alias with longer alias sharing prefix", "sil", ""},
		{"exact match tenant with longer tenants", "tenant", ""},
		{"exact match single only alias", "alerts", ""},
		{"exact match longest in family", "tenants", ""},
		{"exact match single char alias", "q", ""},

		// Case sensitivity matches Resolve's policy.
		{"case sensitive", "ALERT", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, r.Suggest(tc.prefix))
		})
	}
}

func TestResolver_RegisterUserBindsShortToBuiltin(t *testing.T) {
	t.Parallel()

	r := build()
	require.NoError(t, r.RegisterUser("a", "alerts"))

	cmd, err := r.Resolve("a")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, "alerts", msg.Alias,
		"a -> alerts must dispatch to the alerts handler")
}

func TestResolver_RegisterUserPrependsExtraArgs(t *testing.T) {
	t.Parallel()

	// `prod` registered as `tenant prod` must carry "prod" through
	// to the tenant handler — and any further user-typed args land
	// after the alias's stored args.
	r := build()
	require.NoError(t, r.RegisterUser("prod", "tenant prod"))

	cmd, err := r.Resolve("prod staging")
	require.NoError(t, err)
	msg := cmd().(markerMsg)
	require.Equal(t, "tenant", msg.Alias)
	require.Equal(t, []string{"prod", "staging"}, msg.Args)
}

func TestResolver_RegisterUserConflictsWithBuiltin(t *testing.T) {
	t.Parallel()

	r := build()
	err := r.RegisterUser("alerts", "tenant prod")
	require.ErrorIs(t, err, ErrUserAliasConflict)
	require.Contains(t, err.Error(), "alerts")
}

func TestResolver_RegisterUserUnresolvedExpansion(t *testing.T) {
	t.Parallel()

	r := build()
	err := r.RegisterUser("oops", "no-such-builtin")
	require.ErrorIs(t, err, ErrUserAliasUnresolved)
}

func TestResolver_RegisterUserEmptyExpansionFails(t *testing.T) {
	t.Parallel()

	r := build()
	err := r.RegisterUser("oops", "   ")
	require.ErrorIs(t, err, ErrUserAliasUnresolved)
}

func TestResolver_RegisterUserPanicsOnEmptyShort(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		_ = build().RegisterUser("", "alerts")
	})
}

func TestResolver_RegisterUserCannotChainOnUserAlias(t *testing.T) {
	t.Parallel()

	// Once the first RegisterUser snapshots the built-in set,
	// later user aliases can NOT use a previously-registered user
	// alias as their expansion target. This pins behaviour against
	// map-iteration order: a YAML file with `a: b` and
	// `b: alerts` must fail closed regardless of which entry the
	// loader hands to the resolver first.
	r := build()
	require.NoError(t, r.RegisterUser("b", "alerts"),
		"first user alias chains onto a built-in fine")
	err := r.RegisterUser("a", "b")
	require.ErrorIs(t, err, ErrUserAliasUnresolved,
		"a -> b must fail because b was registered as a user alias, not a built-in")
}

func TestResolver_RegisterUserConflictsWithUserAlias(t *testing.T) {
	t.Parallel()

	// The user-vs-user collision is the same fail-closed shape as
	// the user-vs-built-in collision: a typo that re-binds an
	// already-registered short shouldn't silently overwrite.
	r := build()
	require.NoError(t, r.RegisterUser("p", "alerts"))
	err := r.RegisterUser("p", "tenant prod")
	require.ErrorIs(t, err, ErrUserAliasConflict)
}

func TestResolver_RegisterUserAppearsInAliasesAndSuggest(t *testing.T) {
	t.Parallel()

	r := build()
	require.NoError(t, r.RegisterUser("prod", "tenant prod"))

	require.Contains(t, r.Aliases(), "prod",
		"user aliases must show up in Aliases() so the help/picker sees them")
	require.Equal(t, "prod", r.Suggest("pr"),
		"user aliases must participate in ghost-text completion")
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
