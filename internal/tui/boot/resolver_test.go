// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
)

// TestNewResolver_GroupsCatalogue pins the canonical+synonym shape
// the help overlay (commit 7) reads from the resolver. Built-in
// singletons (alerts, config, info, skin, status) get one-name groups; synonym pairs
// (q/quit, silences/sil, receivers/rec, tenant/tenants)
// fold onto a single row each. A future contributor dropping a
// synonym or renaming a canonical fails this test loudly rather
// than silently regressing the COMMANDS column.
func TestNewResolver_GroupsCatalogue(t *testing.T) {
	t.Parallel()

	// Handlers close over *pageEnv but the catalogue assertion only
	// reads the alias names — a zero-value env is sufficient because
	// no handler runs during the test.
	r := newResolver(&pageEnv{})

	require.Equal(t,
		[]cmdbar.AliasGroup{
			{Names: []string{"alerts"}},
			{Names: []string{"config"}},
			{Names: []string{"info"}},
			{Names: []string{"q", "quit"}},
			{Names: []string{"receivers", "rec"}},
			{Names: []string{"reload"}},
			{Names: []string{"silences", "sil"}},
			{Names: []string{"skin"}},
			{Names: []string{"status"}},
			{Names: []string{"tenant", "tenants"}},
		},
		r.Groups())
}

// TestNewResolver_QuitAliases pins that every alias in the quit
// group (the vim-canonical `:q` and the spelled-out `:quit`) emits
// the QuitRequestedMsg precursor, so the page-stack Close cascade
// runs before bubbletea stops. Aliases are read from the catalogue
// so adding a synonym there is covered without restating it here.
func TestNewResolver_QuitAliases(t *testing.T) {
	t.Parallel()

	r := newResolver(&pageEnv{})

	var quit []string
	for _, g := range r.Groups() {
		if g.Names[0] == "q" {
			quit = g.Names
		}
	}
	require.NotEmpty(t, quit, "resolver must register a quit alias group")

	for _, alias := range quit {
		t.Run(alias, func(t *testing.T) {
			t.Parallel()
			cmd, err := r.Resolve(alias)
			require.NoError(t, err)
			require.NotNil(t, cmd)
			require.IsType(t, app.QuitRequestedMsg{}, cmd())
		})
	}
}

// The cmdbar completes on a unique prefix, so a later alias sharing
// a short prefix silently turns a working keystroke sequence into an
// ambiguity error. Each row pins the shortest prefix documented for
// its page.
func TestNewResolver_ShortPrefixesStayUnambiguous(t *testing.T) {
	t.Parallel()

	r := newResolver(&pageEnv{})

	tests := []struct {
		prefix string
		want   []string
	}{
		{prefix: "in", want: []string{"info"}},
		{prefix: "co", want: []string{"config"}},
		{prefix: "sk", want: []string{"skin"}},
		{prefix: "rel", want: []string{"reload"}},
		{prefix: "rec", want: []string{"receivers", "rec"}},
	}

	for _, tc := range tests {
		t.Run(tc.prefix, func(t *testing.T) {
			t.Parallel()

			var matched []string
			for _, g := range r.Groups() {
				for _, name := range g.Names {
					if strings.HasPrefix(name, tc.prefix) {
						matched = append(matched, name)
					}
				}
			}
			require.Equal(t, tc.want, matched)
		})
	}
}

// `:reload` and `:receivers` share two letters, so `:re` resolves to
// neither. The error has to name both, because the user's next move
// is to type the third letter.
func TestNewResolver_ReIsAmbiguousBetweenReceiversAndReload(t *testing.T) {
	t.Parallel()

	r := newResolver(&pageEnv{})

	_, err := r.Resolve("re")

	require.ErrorIs(t, err, cmdbar.ErrAmbiguous)
	require.Contains(t, err.Error(), "receivers")
	require.Contains(t, err.Error(), "reload")
}

// `:skin` is the one verb whose behaviour splits on whether an
// argument is present, so the split is the contract: bare opens the
// picker, named applies straight away without a stop at the picker.
func TestNewResolver_SkinSplitsOnItsArgument(t *testing.T) {
	t.Parallel()

	r := newResolver(&pageEnv{})

	tests := []struct {
		name string
		args []string
		want tea.Msg
	}{
		{name: "bare opens the picker", args: nil, want: app.OpenSkinPickerMsg{}},
		{name: "trailing space still opens the picker", args: []string{"  "}, want: app.OpenSkinPickerMsg{}},
		{name: "named applies directly", args: []string{"nord"}, want: app.ApplySkinMsg{Name: "nord"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd, err := r.Resolve(strings.Join(append([]string{cmdSkin}, tc.args...), " "))
			require.NoError(t, err)
			require.Equal(t, tc.want, cmd())
		})
	}
}
