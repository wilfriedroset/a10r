// SPDX-License-Identifier: Apache-2.0

package config

import "strings"

// ScopeAll is the sentinel scope string that covers every backend.
// Mirrors the `:tenant all` TUI command and the `--tenant all` CLI
// flag value.
const ScopeAll = "all"

// ScopeMatches reports whether tenantName falls within scope. An empty
// scope or ScopeAll covers every backend; otherwise scope is a
// comma-joined exact-match list of backend names. The canonical
// implementation shared by the headless commands and (by delegation)
// the TUI's per-page scope checks, so "all" / "" / single / comma-list
// semantics stay identical across surfaces.
func ScopeMatches(scope, tenantName string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == ScopeAll {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == tenantName {
			return true
		}
	}
	return false
}

// UnknownScopeTenants returns the scope elements that name no configured
// backend, preserving order. An empty scope or ScopeAll has none. Empty
// elements (from a trailing or doubled comma, or surrounding whitespace)
// are ignored, so `prod,` is fine but `prod,bogus` reports `bogus` — a
// scope element that matches nothing is a typo, not a silent narrowing.
func UnknownScopeTenants(backends []Backend, scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == ScopeAll {
		return nil
	}
	known := make(map[string]bool, len(backends))
	for _, b := range backends {
		known[b.Name] = true
	}
	var unknown []string
	for s := range strings.SplitSeq(scope, ",") {
		s = strings.TrimSpace(s)
		if s == "" || known[s] {
			continue
		}
		unknown = append(unknown, s)
	}
	return unknown
}

// ScopeBackends returns the subset of backends whose Name falls within
// scope, preserving input order. An empty scope or ScopeAll returns the
// input slice itself (aliased, not copied — callers must not mutate the
// result); a scope that matches nothing returns an empty slice so
// callers can distinguish "no backend in scope" from "no backends
// configured".
func ScopeBackends(backends []Backend, scope string) []Backend {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == ScopeAll {
		return backends
	}
	out := make([]Backend, 0, len(backends))
	for _, b := range backends {
		if ScopeMatches(scope, b.Name) {
			out = append(out, b)
		}
	}
	return out
}
