// SPDX-License-Identifier: Apache-2.0

// Package cmdbar resolves `:` command-bar input strings into
// tea.Cmds. The resolver is independent of bubbletea wiring — it
// stores alias → handler mappings and looks up by exact-or-unique-
// prefix match. The app shell (#22) opens the prompt on `:` and
// dispatches the resolved Cmd when the prompt submits.
//
// Filter prompts (`/`) bypass this package — their values flow
// straight to the active page, which interprets the syntax in its
// own page-specific way (E1 substring + matcher tokens).
package cmdbar

import (
	"errors"
	"fmt"
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
)

// Handler is the per-alias callback. args carries everything after
// the alias token, split on whitespace — `:tenant prod staging`
// reaches the handler as args=["prod", "staging"]. Empty args
// slice when the user supplied no arguments.
type Handler func(args []string) tea.Cmd

// Resolver stores alias → Handler mappings. Construct via New —
// the zero value is NOT usable because Register would attempt to
// write to a nil map.
type Resolver struct {
	handlers map[string]Handler
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
func (r *Resolver) Register(alias string, h Handler) {
	if alias == "" {
		panic("cmdbar: alias must not be empty")
	}
	if h == nil {
		panic("cmdbar: handler must not be nil")
	}
	r.handlers[alias] = h
}

// Aliases returns the registered alias names sorted alphabetically.
// Today this is consumed only by tests; reserved for the future
// fish-buffer suggestion ring per k9s-look-and-feel.md §3.
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
