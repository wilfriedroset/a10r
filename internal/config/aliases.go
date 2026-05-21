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

// AliasesFile is the basename of the user alias overlay file inside
// the resolved <config-dir>.
const AliasesFile = "aliases.yaml"

// ErrAliasInvalid wraps every validation failure (empty key, value
// containing a newline, …) so callers can branch on a single
// sentinel via errors.Is. Conflict-with-built-in lives on the
// cmdbar resolver (cmdbar.ErrUserAliasConflict) since the resolver
// is the source of truth for the built-in alias set.
var ErrAliasInvalid = errors.New("invalid alias")

// AliasMap is the user-supplied {short -> expanded} mapping
// LoadAliases produces. Empty (no entries) and absent (no file) both
// resolve to an empty, non-nil map — callers iterate without a nil
// check.
type AliasMap map[string]string

// LoadAliases reads <dir>/aliases.yaml and returns the parsed map.
// A missing file is NOT an error: returns an empty AliasMap and nil.
// An empty file (zero entries) is also fine. Only malformed YAML or
// validation failures surface as errors.
//
// dir is the resolved config directory (per ADR 0027). Pass the
// same value the rest of the loader uses; this function does not
// re-resolve it.
func LoadAliases(dir string) (AliasMap, error) {
	if dir == "" {
		return AliasMap{}, nil
	}
	path := filepath.Join(dir, AliasesFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return AliasMap{}, nil
		}
		return nil, fmt.Errorf("read aliases %q: %w", path, err)
	}
	return parseAliases(raw, path)
}

// parseAliases is the I/O-free core of LoadAliases.
func parseAliases(raw []byte, source string) (AliasMap, error) {
	// Whitespace-only is "no aliases" — short-circuit before the
	// decoder so an io.EOF on empty input doesn't surface as a parse
	// error. Comment-only files reach the decoder and surface as EOF
	// post-skip, handled below.
	if len(bytes.TrimSpace(raw)) == 0 {
		return AliasMap{}, nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)

	var m AliasMap
	if err := decoder.Decode(&m); err != nil {
		// io.EOF means the document had no nodes (comment-only or
		// whitespace + comments). Treat the same as empty rather
		// than surfacing a confusing "EOF" parse error to the
		// operator.
		if errors.Is(err, io.EOF) {
			return AliasMap{}, nil
		}
		return nil, fmt.Errorf("parse aliases %q: %w", source, err)
	}
	if m == nil {
		// `aliases.yaml` containing only `null` decodes to a nil map.
		// Treat the same as empty.
		return AliasMap{}, nil
	}
	if err := validateAliases(m); err != nil {
		return nil, fmt.Errorf("validate aliases %q: %w", source, err)
	}
	return m, nil
}

// validateAliases enforces the shape contract: keys are non-empty
// and contain no whitespace (the cmdbar tokenises on whitespace, so
// a key with a space would never match), values are non-empty and
// contain no newline (multi-line expansions would break the prompt
// rendering).
func validateAliases(m AliasMap) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "" {
			return fmt.Errorf("%w: key must not be empty", ErrAliasInvalid)
		}
		if strings.ContainsAny(k, " \t\r\n") {
			return fmt.Errorf("%w: key %q must not contain whitespace", ErrAliasInvalid, k)
		}
		v := m[k]
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%w: value for %q must not be empty", ErrAliasInvalid, k)
		}
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%w: value for %q must not contain newlines", ErrAliasInvalid, k)
		}
	}
	return nil
}
