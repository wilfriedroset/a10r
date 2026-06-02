// SPDX-License-Identifier: Apache-2.0

// Package cmdbar resolves `:` command-bar input strings into
// tea.Cmds. The resolver is independent of bubbletea wiring — it
// stores alias → handler mappings and looks up by exact-or-unique-
// prefix match. The app shell opens the prompt on `:` and
// dispatches the resolved Cmd when the prompt submits.
//
// Filter prompts (`/`) bypass this package — their values flow
// straight to the active page, which interprets the syntax in its
// own page-specific way (substring + matcher tokens).
package cmdbar

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Sentinel errors so callers can route them to specific UX paths
// (e.g. flash with a specific level / suggestion).
var (
	// ErrEmpty is returned when the input has no tokens after
	// trimming whitespace — the user pressed Enter on `:` with
	// nothing else.
	ErrEmpty = errors.New("empty command")
	// ErrUnknown is returned when no alias matches the input
	// (neither exactly nor as a unique prefix).
	ErrUnknown = errors.New("unknown command")
	// ErrAmbiguous is returned when the input is a non-unique
	// prefix of two or more registered aliases. The error string
	// lists the candidates so the caller can surface them.
	ErrAmbiguous = errors.New("ambiguous command")
	// ErrUserAliasConflict is returned by RegisterUser when the
	// short already exists in the built-in alias set. Fail-closed
	// so a user typo can't shadow a registered binding silently —
	// same approach as the keybinding-conflict handling.
	ErrUserAliasConflict = errors.New("user alias conflicts with built-in")
	// ErrUserAliasUnresolved is returned by RegisterUser when the
	// alias's expanded value doesn't resolve to a known built-in.
	// Caught at startup so the error is loud rather than surfacing
	// the first time the user types the alias.
	ErrUserAliasUnresolved = errors.New("user alias expansion unresolved")
)

// Handler is the per-alias callback. args carries everything after
// the alias token, split on whitespace — `:tenant prod staging`
// reaches the handler as args=["prod", "staging"]. Empty args
// slice when the user supplied no arguments.
type Handler func(args []string) tea.Cmd

// AliasGroup is the set of names sharing one handler. Singleton
// groups come from Register; multi-name groups from RegisterGroup.
// The first entry in Names is the canonical name and is what the
// help overlay surfaces as the row header (the remaining names are
// folded onto the same row as `canonical, short` per ADR 0038).
type AliasGroup struct {
	Names []string
}

// UserAlias is one user-defined shorthand registered via
// RegisterUser. The help overlay renders these in a dedicated USER
// subsection as `short → expanded` so a reader can tell at a glance
// which aliases come from their own config and what each maps to.
//
// Expanded is the user-typed expansion with leading/trailing
// whitespace trimmed so the help overlay never paints stray
// padding; runs of internal whitespace are preserved verbatim so
// `prod   staging` reads back exactly as the operator wrote it.
type UserAlias struct {
	Short    string
	Expanded string
}

// Resolver stores alias → Handler mappings. Construct via New —
// the zero value is NOT usable because Register would attempt to
// write to a nil map.
//
// builtins, when non-nil, is the snapshot of alias names taken at
// the moment RegisterUser was first called. It pins the set of
// valid expansion targets so user aliases cannot transitively chain
// onto each other in a registration-order-dependent way.
type Resolver struct {
	handlers map[string]Handler
	// groups is the help-overlay-facing registration ledger. Each
	// Register call appends a singleton; each RegisterGroup call
	// appends a multi-name entry. Re-registering an alias prunes any
	// prior entry that mentions that alias before appending the new
	// row — last-write-wins, so Groups() never surfaces a stale row
	// next to a fresh one. Independent of `handlers` so the help
	// overlay can fold synonyms without poking at Go function values.
	groups []AliasGroup
	// userAliases keeps the raw (short, expanded) pairs captured by
	// RegisterUser so the help overlay's USER subsection can render
	// them. Separate from `groups` because user aliases bind extra
	// args and folding them onto a built-in row would mislead.
	userAliases []UserAlias
	builtins    map[string]struct{}
}

// New constructs an empty Resolver.
func New() *Resolver {
	return &Resolver{handlers: map[string]Handler{}}
}

// Register binds an alias to a handler. Re-registering an alias
// overwrites silently — callers are expected to validate at
// configuration time, the resolver is the source of truth at
// runtime. Empty aliases panic because they indicate programmer
// error rather than a runtime condition.
//
// Each Register call appends a singleton AliasGroup so Groups()
// surfaces every built-in row to the help overlay regardless of
// whether the caller used Register or RegisterGroup. Any prior
// group row that mentioned this alias is dropped — Groups() must
// never surface a stale singleton next to the fresh registration.
func (r *Resolver) Register(alias string, h Handler) {
	if alias == "" {
		panic("cmdbar: alias must not be empty")
	}
	if h == nil {
		panic("cmdbar: handler must not be nil")
	}
	r.handlers[alias] = h
	r.dropGroupsContaining(alias)
	r.groups = append(r.groups, AliasGroup{Names: []string{alias}})
}

// RegisterGroup binds every name in `names` to the same handler.
// The first entry is canonical (what the help overlay shows as the
// row header); remaining entries fold onto that row as synonyms
// per ADR 0038. Use Register for singleton aliases — RegisterGroup
// is reserved for genuine synonyms (silences/sil, groups/gr, ...).
//
// Empty names slice / empty individual name / nil handler / a name
// repeated within `names` all panic because each indicates
// programmer error in the caller.
//
// Re-registering any name in `names` prunes prior group rows that
// mentioned it (mirroring Register's silent-overwrite contract),
// so a Register("sil", ...) followed by RegisterGroup(["silences",
// "sil"], ...) leaves Groups() with one row — the multi-name group.
func (r *Resolver) RegisterGroup(names []string, h Handler) {
	if len(names) == 0 {
		panic("cmdbar: alias group must not be empty")
	}
	if h == nil {
		panic("cmdbar: handler must not be nil")
	}
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n == "" {
			panic("cmdbar: alias must not be empty")
		}
		if _, dup := seen[n]; dup {
			panic("cmdbar: duplicate alias in group: " + n)
		}
		seen[n] = struct{}{}
	}
	for _, n := range names {
		r.handlers[n] = h
		r.dropGroupsContaining(n)
	}
	// Copy so a later caller mutation of the input slice can't
	// rewrite the group's recorded order.
	copied := make([]string, len(names))
	copy(copied, names)
	r.groups = append(r.groups, AliasGroup{Names: copied})
}

// Groups returns built-in alias groups sorted alphabetically by
// canonical name (first entry of each group). User aliases live in
// UserAliases() — folding them here would mislead the help overlay
// reader, since a user alias may bind extra args its target does
// not.
//
// Deep-copies every group's Names slice so a caller can't mutate
// the resolver's internal state through the returned value.
func (r *Resolver) Groups() []AliasGroup {
	out := make([]AliasGroup, len(r.groups))
	for i, g := range r.groups {
		names := make([]string, len(g.Names))
		copy(names, g.Names)
		out[i] = AliasGroup{Names: names}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Names[0] < out[j].Names[0]
	})
	return out
}

// dropGroupsContaining prunes every group row that mentions
// `alias`. Linear in the registration count; the catalogue stays
// small (single-digit groups) so a smarter index is not worth the
// complexity.
func (r *Resolver) dropGroupsContaining(alias string) {
	kept := r.groups[:0]
	for _, g := range r.groups {
		if slices.Contains(g.Names, alias) {
			continue
		}
		kept = append(kept, g)
	}
	r.groups = kept
}

// Aliases returns the registered alias names sorted alphabetically.
// Today this is consumed only by tests; reserved for the future
// fish-buffer style suggestion ring.
func (r *Resolver) Aliases() []string {
	out := make([]string, 0, len(r.handlers))
	for a := range r.handlers {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Resolve parses input and returns the resulting tea.Cmd. Matching
// rules, in order:
//
//  1. Empty / whitespace-only → ErrEmpty.
//  2. Exact match on the first whitespace-separated token →
//     handler runs with the remaining tokens as args.
//  3. Unique prefix match → same as exact, with the prefix
//     resolved to the full alias.
//  4. Multiple prefix matches → ErrAmbiguous wrapped with the
//     candidate list.
//  5. No prefix match → ErrUnknown wrapped with the input.
//
// The prefix path is what makes `:sil` resolve to `:silences`
// per keybindings.md / cmd alias catalogue.
func (r *Resolver) Resolve(input string) (tea.Cmd, error) {
	tokens := strings.Fields(input)
	if len(tokens) == 0 {
		return nil, ErrEmpty
	}
	alias, args := tokens[0], tokens[1:]
	if h, ok := r.handlers[alias]; ok {
		return h(args), nil
	}
	matches := r.prefixMatches(alias)
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w: %s", ErrUnknown, alias)
	case 1:
		return r.handlers[matches[0]](args), nil
	default:
		return nil, fmt.Errorf("%w: %s — %s", ErrAmbiguous, alias, strings.Join(matches, ", "))
	}
}

// Suggest returns the alphabetically-first registered alias that
// has `prefix` as a prefix, for ghost-text completion in the `:`
// prompt. Returns "" (no ghost) when prefix is empty, no alias
// starts with prefix, or prefix exactly equals an alias (even
// when a longer alias shares the prefix — Tab on a typed-in-full
// alias must not auto-extend past it).
//
// Alphabetical tie-breaking is deterministic and always yields a
// ghost when any match exists, unlike longest-unique-prefix.
// Mirrors k9s's "tab accepts first suggestion" affordance.
// Callers compute the visible suffix by trimming the prefix from
// the returned alias.
func (r *Resolver) Suggest(prefix string) string {
	if prefix == "" {
		return ""
	}
	if _, ok := r.handlers[prefix]; ok {
		return ""
	}
	matches := r.prefixMatches(prefix)
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// RegisterUser binds a user-supplied alias `short` to the same
// handler as `expanded`. Conflicts (short already registered as
// either a built-in or a previously-registered user alias) and
// unknown expansions (expanded targets no built-in) are fail-closed
// so problems surface at startup rather than the first time the
// user types the alias.
//
// On the first RegisterUser call, the resolver snapshots the
// current alias set as the "built-in" set; only those names are
// valid expansion targets afterwards. This pins behaviour against
// the Go map-iteration order: a user file mixing
// `a: b` and `b: tenant prod` cannot succeed-or-fail based on which
// entry was registered first.
//
// The expanded value is tokenised the same way Resolve treats user
// input: the first whitespace-separated token is the built-in to
// chain into; any remaining tokens are pre-pended to whatever args
// the user types at the prompt. This lets `prod: tenant prod`
// register a shorthand that always carries the `prod` argument.
//
// Empty short panics — same reasoning as Register: an empty alias
// indicates programmer error in the caller, not a runtime
// condition.
func (r *Resolver) RegisterUser(short, expanded string) error {
	if short == "" {
		panic("cmdbar: user alias short must not be empty")
	}
	if r.builtins == nil {
		r.builtins = make(map[string]struct{}, len(r.handlers))
		for a := range r.handlers {
			r.builtins[a] = struct{}{}
		}
	}
	if _, taken := r.handlers[short]; taken {
		return fmt.Errorf("%w: %s", ErrUserAliasConflict, short)
	}
	tokens := strings.Fields(expanded)
	if len(tokens) == 0 {
		return fmt.Errorf("%w: %s -> %q", ErrUserAliasUnresolved, short, expanded)
	}
	target, extra := tokens[0], tokens[1:]
	if _, ok := r.builtins[target]; !ok {
		return fmt.Errorf("%w: %s -> %s", ErrUserAliasUnresolved, short, target)
	}
	base := r.handlers[target]
	r.handlers[short] = func(args []string) tea.Cmd {
		// Pre-pend the alias's stored args so `prod` registered as
		// `tenant prod` always passes "prod" through. Anything the
		// user types after the alias at the prompt comes after.
		merged := make([]string, 0, len(extra)+len(args))
		merged = append(merged, extra...)
		merged = append(merged, args...)
		return base(merged)
	}
	r.userAliases = append(r.userAliases, UserAlias{Short: short, Expanded: strings.TrimSpace(expanded)})
	return nil
}

// UserAliases returns the user-registered aliases sorted
// alphabetically by Short. The returned slice is a fresh allocation
// so a caller can append / overwrite freely without touching
// resolver state.
func (r *Resolver) UserAliases() []UserAlias {
	out := make([]UserAlias, len(r.userAliases))
	copy(out, r.userAliases)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Short < out[j].Short
	})
	return out
}

// prefixMatches returns every registered alias that starts with
// the given prefix, sorted alphabetically.
func (r *Resolver) prefixMatches(prefix string) []string {
	var out []string
	for a := range r.handlers {
		if strings.HasPrefix(a, prefix) {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}
