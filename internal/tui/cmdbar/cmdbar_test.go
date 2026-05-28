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

func TestResolver_IsCaseSensitive(t *testing.T) {
	t.Parallel()

	// Aliases match exactly. "ALERTS" must not resolve to "alerts".
	// This is intentional so a future :TENANT vs :tenant ergonomic
	// decision (left for a downstream commit) stays a design call,
	// not an accident of the resolver's matching rule.
	_, err := build().Resolve("ALERTS")
	require.ErrorIs(t, err, ErrUnknown)
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

func TestResolver_RegisterUserAppearsInAliasesAndSuggest(t *testing.T) {
	t.Parallel()

	r := build()
	require.NoError(t, r.RegisterUser("prod", "tenant prod"))

	require.Contains(t, r.Aliases(), "prod",
		"user aliases must show up in Aliases() so the help/picker sees them")
	require.Equal(t, "prod", r.Suggest("pr"),
		"user aliases must participate in ghost-text completion")
}

func TestResolver_RegisterGroupBindsEveryName(t *testing.T) {
	t.Parallel()

	r := New()
	r.RegisterGroup([]string{"silences", "sil"}, func(args []string) tea.Cmd {
		return func() tea.Msg { return markerMsg{Alias: "silences", Args: args} }
	})

	for _, name := range []string{"silences", "sil"} {
		cmd, err := r.Resolve(name)
		require.NoError(t, err)
		require.Equal(t, "silences", cmd().(markerMsg).Alias,
			"every name in the group routes to the same handler")
	}
}

func TestResolver_GroupsReturnsCanonicalFirst(t *testing.T) {
	t.Parallel()

	r := New()
	r.RegisterGroup([]string{"silences", "sil"}, func(_ []string) tea.Cmd { return nil })
	r.RegisterGroup([]string{"groups", "gr"}, func(_ []string) tea.Cmd { return nil })
	r.Register("alerts", func(_ []string) tea.Cmd { return nil })

	got := r.Groups()
	require.Equal(t,
		[]AliasGroup{
			{Names: []string{"alerts"}},
			{Names: []string{"groups", "gr"}},
			{Names: []string{"silences", "sil"}},
		},
		got,
		"groups sort by canonical (first name); singletons from Register live alongside multi-name groups")
}

func TestResolver_GroupsExcludesUserAliases(t *testing.T) {
	t.Parallel()

	// User aliases chain into a built-in with possibly bound args
	// (e.g. `prod -> tenant prod`). They are a specialisation, not
	// a synonym, so Groups() must not fold them onto the built-in's
	// row. The help overlay renders them in their own USER section.
	r := New()
	r.RegisterGroup([]string{"tenant", "tenants"}, func(_ []string) tea.Cmd { return nil })
	require.NoError(t, r.RegisterUser("prod", "tenant prod"))

	got := r.Groups()
	require.Equal(t,
		[]AliasGroup{{Names: []string{"tenant", "tenants"}}},
		got,
		"Groups() reflects built-in registrations only; user aliases live in UserAliases()")
}

func TestResolver_RegisterGroupPanicsOnEmptyNames(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		New().RegisterGroup(nil, func(_ []string) tea.Cmd { return nil })
	})
	require.Panics(t, func() {
		New().RegisterGroup([]string{}, func(_ []string) tea.Cmd { return nil })
	})
}

func TestResolver_RegisterGroupPanicsOnEmptyName(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		New().RegisterGroup([]string{"silences", ""}, func(_ []string) tea.Cmd { return nil })
	})
}

func TestResolver_RegisterGroupPanicsOnNilHandler(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		New().RegisterGroup([]string{"silences"}, nil)
	})
}

func TestResolver_RegisterGroupPanicsOnDuplicateName(t *testing.T) {
	t.Parallel()

	// Duplicate names within a single RegisterGroup call would render
	// as `sil, sil` in the help overlay — a clear programmer error
	// rather than a runtime condition. Panic fast at the seam.
	require.Panics(t, func() {
		New().RegisterGroup([]string{"sil", "sil"}, func(_ []string) tea.Cmd { return nil })
	})
}

func TestResolver_RegisterDedupsPriorGroup(t *testing.T) {
	t.Parallel()

	// Register's existing contract is "silent overwrite": calling it
	// twice for the same alias must leave Groups() with exactly one
	// row for that alias, pointing at the latest handler. Otherwise
	// the help overlay would surface a stale singleton next to the
	// fresh one.
	r := New()
	r.Register("alerts", func(_ []string) tea.Cmd {
		return func() tea.Msg { return markerMsg{Alias: "first"} }
	})
	r.Register("alerts", func(_ []string) tea.Cmd {
		return func() tea.Msg { return markerMsg{Alias: "second"} }
	})

	require.Equal(t, []AliasGroup{{Names: []string{"alerts"}}}, r.Groups(),
		"re-Register must leave exactly one Groups() row")
	cmd, err := r.Resolve("alerts")
	require.NoError(t, err)
	require.Equal(t, "second", cmd().(markerMsg).Alias,
		"re-Register handler must be the latest")
}

func TestResolver_RegisterGroupSupersedesPriorRegister(t *testing.T) {
	t.Parallel()

	// Common boot ordering quirk: a one-name Register is later
	// replaced by a RegisterGroup that folds the same alias under a
	// canonical. The earlier singleton row must NOT linger.
	r := New()
	r.Register("sil", func(_ []string) tea.Cmd { return nil })
	r.RegisterGroup([]string{"silences", "sil"}, func(_ []string) tea.Cmd { return nil })

	require.Equal(t,
		[]AliasGroup{{Names: []string{"silences", "sil"}}},
		r.Groups(),
		"RegisterGroup must prune any prior singleton row that mentions any of its names")
}

func TestResolver_RegisterGroupSupersedesPriorGroup(t *testing.T) {
	t.Parallel()

	// Re-registering a group must drop any prior group row that
	// shared even one name — last-write-wins applies at the row
	// level, not per name, because two rows for overlapping names
	// would mislead the help overlay reader.
	r := New()
	r.RegisterGroup([]string{"silences", "sil"}, func(_ []string) tea.Cmd { return nil })
	r.RegisterGroup([]string{"sil", "silencer"}, func(_ []string) tea.Cmd { return nil })

	require.Equal(t,
		[]AliasGroup{{Names: []string{"sil", "silencer"}}},
		r.Groups(),
		"second RegisterGroup must prune the first because they overlap on `sil`")
}

func TestResolver_GroupsResistsInputMutation(t *testing.T) {
	t.Parallel()

	// The names slice the caller passes is defensively copied at
	// registration time so a later mutation of the source slice can
	// not rewrite the group's recorded order in the help overlay.
	r := New()
	names := []string{"silences", "sil"}
	r.RegisterGroup(names, func(_ []string) tea.Cmd { return nil })
	names[0] = "MUTATED"
	names[1] = "ALSO-MUTATED"

	require.Equal(t,
		[]AliasGroup{{Names: []string{"silences", "sil"}}},
		r.Groups(),
		"Groups() must be insulated from post-call input-slice mutation")
}

func TestResolver_GroupsResistsOutputMutation(t *testing.T) {
	t.Parallel()

	// Symmetric to the input-mutation defence: Groups() must return
	// a deep copy so a caller mutating any Names entry post-call
	// can not rewrite the resolver's internal state.
	r := New()
	r.RegisterGroup([]string{"silences", "sil"}, func(_ []string) tea.Cmd { return nil })

	out := r.Groups()
	out[0].Names[0] = "MUTATED"

	require.Equal(t,
		[]AliasGroup{{Names: []string{"silences", "sil"}}},
		r.Groups(),
		"resolver state must survive caller mutation of a previous Groups() return")
}

func TestResolver_RegisterAndRegisterGroupSingletonAreEquivalent(t *testing.T) {
	t.Parallel()

	// Register(alias, h) and RegisterGroup([]string{alias}, h) must
	// produce identical Groups() rows so the help overlay never sees
	// a shape difference between the two registration paths.
	r1 := New()
	r1.Register("alerts", func(_ []string) tea.Cmd { return nil })
	r2 := New()
	r2.RegisterGroup([]string{"alerts"}, func(_ []string) tea.Cmd { return nil })

	require.Equal(t, r1.Groups(), r2.Groups(),
		"Register and single-element RegisterGroup must produce equivalent Groups() output")
}
