// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// KeysDir is the basename of the per-profile keybinding overlay
// directory inside the resolved <config-dir>. Matches the C3 schema
// in docs/design/phase-2-plan.md (P2.W1.5) and ADR 0010.
const KeysDir = "keys"

// DefaultKeysProfile is the auto-loaded profile name. v0.0.1 only
// auto-loads `default.yaml`; explicit profile selection (e.g. `vim`)
// is a stretch goal noted in ADR 0010 — the loader already supports
// the path argument so wiring it through `keys: { profile: ... }`
// later is purely additive.
const DefaultKeysProfile = "default"

// reservedKeys is the closed set of keys the user is NOT allowed
// to bind in their overlay. 0-9 are reserved for the C3 tenant
// quick-switch (`<0>` = all, `<1>`-`<9>` = the Nth backend) — the
// whole point of the quick-switch is muscle memory, so re-purposing
// any of these keys silently is a worse failure mode than refusing
// to start. The list is intentionally tiny in v0.0.1; future
// reservations land here with a corresponding ADR amendment.
var reservedKeys = map[string]struct{}{
	"0": {}, "1": {}, "2": {}, "3": {}, "4": {},
	"5": {}, "6": {}, "7": {}, "8": {}, "9": {},
}

// ErrKeyOverrideInvalid wraps every validation failure (reserved key,
// same-file conflict, malformed entry) so callers can branch on a
// single sentinel via errors.Is. Unknown-action detection lives in
// the dispatcher (internal/tui/keys/dispatch.go) since the action
// catalog is registered there at startup; the loader stays oblivious
// to which actions exist.
var ErrKeyOverrideInvalid = errors.New("invalid key override")

// KeyOverrides is the action-name → user-extra-keys map LoadKeys
// produces. "Shadow defaults" semantics (per ADR 0010): the user
// keys are ADDITIONAL bindings layered on top of the action's
// built-in default — they never replace it. Empty (no entries) and
// absent (no file) both resolve to an empty, non-nil map so callers
// iterate without a nil check.
type KeyOverrides map[string][]string

// LoadKeys reads <dir>/keys/<profile>.yaml and returns the parsed
// overrides. A missing file (or empty `dir`) is NOT an error:
// returns an empty KeyOverrides and nil. Empty profile name falls
// back to DefaultKeysProfile so production callers can pass a
// pre-resolved profile string straight from the config without
// branching on the empty case.
//
// dir is the resolved config directory (per K1/B2 precedence). Pass
// the same value the rest of the loader uses; this function does
// not re-resolve it.
func LoadKeys(dir, profile string) (KeyOverrides, error) {
	if dir == "" {
		return KeyOverrides{}, nil
	}
	if profile == "" {
		profile = DefaultKeysProfile
	}
	path := filepath.Join(dir, KeysDir, profile+".yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return KeyOverrides{}, nil
		}
		return nil, fmt.Errorf("read keys %q: %w", path, err)
	}
	return parseKeys(raw, path)
}

// parseKeys is the I/O-free core of LoadKeys. Pulled out so tests
// can drive every branch without writing to disk.
//
// Decoded into a yaml.Node tree (rather than directly into a map)
// so the validator can quote the exact line:column of each binding
// in error messages — the operator opens their editor at the right
// spot rather than hunting through the file.
func parseKeys(raw []byte, source string) (KeyOverrides, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return KeyOverrides{}, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		if errors.Is(err, io.EOF) {
			return KeyOverrides{}, nil
		}
		return nil, fmt.Errorf("parse keys %q: %w", source, err)
	}
	root := documentRoot(&doc)
	if root == nil || isNullScalar(root) {
		return KeyOverrides{}, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: %s:%d: top-level must be a mapping of action -> [keys...]",
			ErrKeyOverrideInvalid, source, root.Line)
	}
	return validateKeyMapping(root, source)
}

// validateKeyMapping walks every (action, [keys...]) pair under the
// root mapping, checking the reserved-key carve-out and same-file
// conflicts inline. The first occurrence of a key claims the slot;
// the second triggers the error pointing at the OFFENDING line so
// the operator opens their editor at the line they need to fix.
func validateKeyMapping(root *yaml.Node, source string) (KeyOverrides, error) {
	out := KeyOverrides{}
	type binding struct {
		action string
		line   int
	}
	keyOwner := map[string]binding{}

	for i := 0; i < len(root.Content); i += 2 {
		action, err := decodeActionName(root.Content[i], source)
		if err != nil {
			return nil, err
		}
		keys, err := decodeKeyList(root.Content[i+1], source)
		if err != nil {
			return nil, err
		}
		for _, kv := range keys {
			if _, reserved := reservedKeys[kv.value]; reserved {
				return nil, fmt.Errorf(
					"%w: %s:%d: %q attempts to bind reserved key %q (0-9 are reserved for tenant quick-switch)",
					ErrKeyOverrideInvalid, source, kv.line, action, kv.value)
			}
			if prev, dup := keyOwner[kv.value]; dup && prev.action != action {
				return nil, fmt.Errorf(
					"%w: %s:%d: key %q is also bound to action %q at line %d",
					ErrKeyOverrideInvalid, source, kv.line, kv.value, prev.action, prev.line)
			}
			keyOwner[kv.value] = binding{action: action, line: kv.line}
			out[action] = append(out[action], kv.value)
		}
	}

	// Stable order so downstream registration (and tests) see the
	// same shape regardless of map iteration order. The map preserves
	// per-action insertion order naturally; we just sort each slice
	// so two-key bindings on the same action don't flap.
	for _, ks := range out {
		sort.Strings(ks)
	}
	return out, nil
}

// decodeActionName pulls the action string out of an action key
// node, surfacing the two failure modes (non-scalar, empty after
// trim) with file:line precision.
func decodeActionName(n *yaml.Node, source string) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("%w: %s:%d: action name must be a scalar string",
			ErrKeyOverrideInvalid, source, n.Line)
	}
	action := strings.TrimSpace(n.Value)
	if action == "" {
		return "", fmt.Errorf("%w: %s:%d: action name must not be empty",
			ErrKeyOverrideInvalid, source, n.Line)
	}
	return action, nil
}

// isNullScalar reports whether n is a YAML null in any of the shapes
// the spec recognises. Used so a `null` / `~` / empty top-level
// resolves to "no overrides" rather than a parse error.
func isNullScalar(n *yaml.Node) bool {
	if n.Kind != yaml.ScalarNode {
		return false
	}
	return n.Tag == "!!null" || n.Value == "null" || n.Value == "~" || n.Value == ""
}

// keyEntry pairs a key string with the YAML line it was declared on
// so conflict / reserved-key errors can quote the operator's exact
// source line.
type keyEntry struct {
	value string
	line  int
}

// decodeKeyList accepts a sequence of scalar keys (the canonical
// schema) or a single scalar (sugar for one-key bindings — `quit:
// Q` is identical to `quit: [Q]`). Anything else is a hard error.
//
// Each accepted scalar is canonicalised via canonicaliseKey so the
// user-facing spellings the operator naturally reaches for (`Q`,
// `shift+q`, `ctrl+x`, `alt+space`) all reach the dispatcher in the
// title-case Ctrl/Alt/Shift form the bubbletea normaliser
// (internal/tui/app/keys.go) emits at runtime. Without this
// rewrite, a bare uppercase `Q` would never fire — bubbletea v2
// reports a shifted letter as `Shift+Q`, not `Q`.
func decodeKeyList(n *yaml.Node, source string) ([]keyEntry, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		v := strings.TrimSpace(n.Value)
		if v == "" {
			return nil, fmt.Errorf("%w: %s:%d: key must not be empty",
				ErrKeyOverrideInvalid, source, n.Line)
		}
		return []keyEntry{{value: canonicaliseKey(v), line: n.Line}}, nil
	case yaml.SequenceNode:
		out := make([]keyEntry, 0, len(n.Content))
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("%w: %s:%d: key entries must be scalars",
					ErrKeyOverrideInvalid, source, item.Line)
			}
			v := strings.TrimSpace(item.Value)
			if v == "" {
				return nil, fmt.Errorf("%w: %s:%d: key must not be empty",
					ErrKeyOverrideInvalid, source, item.Line)
			}
			out = append(out, keyEntry{value: canonicaliseKey(v), line: item.Line})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s:%d: value must be a string or list of strings",
			ErrKeyOverrideInvalid, source, n.Line)
	}
}

// canonicaliseKey maps a user-typed key string to the canonical form
// the dispatcher binds against — the same form `normalizeKey`
// produces from a runtime tea.KeyPressMsg. Two reshapes happen here:
//
//  1. A bare uppercase letter (`Q`, `G`) is rewritten to `Shift+<X>`.
//     Bubbletea v2 reports a shifted letter as `Shift+Q`, never as
//     the bare letter, so without this hop `quit: ['Q']` would
//     register a binding nothing ever fires.
//  2. Modifier prefixes typed lowercase (`shift+q`, `ctrl+x`,
//     `alt+space`) are title-cased to match keybindings.md's table
//     (`Shift+Q`, `Ctrl+X`, `Alt+Space`) so the YAML can spell the
//     binding either way and both reach the same destination.
//
// Everything else (single lowercase letters, named keys like `Esc`,
// chord prefixes `gg`) passes through unchanged — the runtime
// dispatcher already keys those identically.
func canonicaliseKey(k string) string {
	if k == "" {
		return k
	}
	// Bare ASCII uppercase letter: rewrite to Shift+<X>.
	if len(k) == 1 && k[0] >= 'A' && k[0] <= 'Z' {
		return "Shift+" + k
	}
	if !strings.Contains(k, "+") {
		return k
	}
	parts := strings.Split(k, "+")
	last := parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	rewritten := make([]string, 0, len(mods)+1)
	for _, m := range mods {
		switch strings.ToLower(m) {
		case "ctrl":
			rewritten = append(rewritten, "Ctrl")
		case "alt":
			rewritten = append(rewritten, "Alt")
		case "shift":
			rewritten = append(rewritten, "Shift")
		default:
			// Unknown modifier: leave the whole token alone so the
			// dispatcher's lookup miss surfaces it verbatim rather
			// than silently masking a typo.
			return k
		}
	}
	// Final segment: title-case single letters (`q` → `Q`) so the
	// "Shift+Q vs Shift+q" mismatch can't bite. Named special keys
	// (`space`, `esc`, `enter`, …) are title-cased to match the
	// dispatcher's specialNames catalogue so `alt+space` reaches
	// `Alt+Space` rather than `Alt+space`.
	switch {
	case len(last) == 1 && last[0] >= 'a' && last[0] <= 'z':
		last = strings.ToUpper(last)
	case canonicalNamedKey(last) != "":
		last = canonicalNamedKey(last)
	}
	rewritten = append(rewritten, last)
	return strings.Join(rewritten, "+")
}

// namedKeyCanonical maps the lower-case spelling of every supported
// special key to the title-case form the dispatcher's specialNames
// catalogue uses. A package-level table beats a switch for two
// reasons: the gocyclo linter complains about a 15+ branch switch
// for what is really a flat lookup, and a table can be inspected /
// extended by future code without touching control flow.
var namedKeyCanonical = map[string]string{
	"space":     "Space",
	"esc":       "Esc",
	"enter":     "Enter",
	"tab":       "Tab",
	"backspace": "Backspace",
	"up":        "Up",
	"down":      "Down",
	"left":      "Left",
	"right":     "Right",
	"home":      "Home",
	"end":       "End",
	"pgup":      "PgUp",
	"pgdown":    "PgDown",
	"delete":    "Delete",
	"insert":    "Insert",
}

// canonicalNamedKey title-cases a special-key name to match the
// dispatcher's specialNames catalogue. Returns "" when the input
// is not a recognised special key — callers leave the segment
// untouched in that case so an unknown name surfaces verbatim
// rather than being silently mangled.
func canonicalNamedKey(s string) string {
	return namedKeyCanonical[strings.ToLower(s)]
}

// documentRoot strips the outer DocumentNode wrapper yaml.v3 returns
// when decoding into a yaml.Node and surfaces the actual content
// node. Returns nil when the document carries no content (comment-
// only files, fully-empty input) so callers can treat that as "empty
// file".
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}
