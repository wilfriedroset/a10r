// SPDX-License-Identifier: Apache-2.0

// Package theme parses palette+roles skin files (per M1 / theming.md)
// and compiles them into a Styles struct of lipgloss.Style values
// the rest of the TUI consumes by role name (e.g. styles.Table.Cursor).
//
// Schema is two-layer: a per-theme palette of named colours, then a
// fixed-shape roles map binding semantic UI slots to palette entries.
// Three bundled skins ship via embed.FS; users override with
// <config-dir>/skins/<name>.yaml per B2.
package theme

import (
	"errors"
	"fmt"
	"regexp"
)

// hexColorPattern matches `#rrggbb` 6-digit hex colours. Per M1 8-digit
// hex (with alpha) is not supported by terminal renderers — the
// loader rejects it.
var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// skinFile mirrors the YAML schema in theming.md. Pulled out as
// unexported so callers go through Loader, which validates and
// compiles in one pass.
type skinFile struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Author      string            `yaml:"author,omitempty"`
	Palette     map[string]string `yaml:"palette"`
	Roles       roleSet           `yaml:"roles"`
}

// roleSet is the fixed-shape role map every theme must populate.
// Field tags match theming.md; missing roles surface as the zero
// value of the corresponding role struct, which the compiler
// treats as "undefined palette ref".
type roleSet struct {
	Body         fgBgRole         `yaml:"body"`
	Header       headerRole       `yaml:"header"`
	Table        tableRole        `yaml:"table"`
	Severity     severityRole     `yaml:"severity"`
	SilenceState silenceStateRole `yaml:"silence_state"`
	Prompt       promptRole       `yaml:"prompt"`
	Flash        flashRole        `yaml:"flash"`
	Crumbs       crumbsRole       `yaml:"crumbs"`
	Hint         hintRole         `yaml:"hint"`
	Modal        modalRole        `yaml:"modal"`
	YAML         yamlRole         `yaml:"yaml"`
}

// fgBgRole is the simple fg+bg shape used by `body`. Other multi-
// part roles have their own dedicated structs.
type fgBgRole struct {
	Fg string `yaml:"fg"`
	Bg string `yaml:"bg"`
}

type headerRole struct {
	Fg     string `yaml:"fg"`
	Bg     string `yaml:"bg"`
	Accent string `yaml:"accent"`
	OK     string `yaml:"ok"`
	Warn   string `yaml:"warn"`
	Error  string `yaml:"error"`
}

type tableRole struct {
	Header       fgBgRole `yaml:"header"`
	HeaderActive fgBgRole `yaml:"header_active"`
	Row          fgBgRole `yaml:"row"`
	RowAlt       fgBgRole `yaml:"row_alt"`
	Cursor       fgBgRole `yaml:"cursor"`
	Marked       fgBgRole `yaml:"marked"`
	Dimmed       fgBgRole `yaml:"dimmed"`
}

type severityRole struct {
	Critical string `yaml:"critical"`
	Warning  string `yaml:"warning"`
	Info     string `yaml:"info"`
	Unknown  string `yaml:"unknown"`
}

type silenceStateRole struct {
	Active  string `yaml:"active"`
	Pending string `yaml:"pending"`
	Expired string `yaml:"expired"`
}

type promptRole struct {
	Fg         string `yaml:"fg"`
	Bg         string `yaml:"bg"`
	Suggestion string `yaml:"suggestion"`
}

type flashRole struct {
	Success string `yaml:"success"`
	Info    string `yaml:"info"`
	Warn    string `yaml:"warn"`
	Error   string `yaml:"error"`
}

type crumbsRole struct {
	Fg     string `yaml:"fg"`
	Bg     string `yaml:"bg"`
	Active string `yaml:"active"`
}

type hintRole struct {
	Fg      string `yaml:"fg"`
	Bg      string `yaml:"bg"`
	Key     string `yaml:"key"`
	HelpKey string `yaml:"help_key"`
}

type modalRole struct {
	Fg     string `yaml:"fg"`
	Bg     string `yaml:"bg"`
	Border string `yaml:"border"`
}

type yamlRole struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
	Punct string `yaml:"punct"`
}

// validate checks the file-level invariants (name present, palette
// values are well-formed hex). Role-level validation is folded into
// compile (which needs the palette anyway).
func (s skinFile) validate() error {
	if s.Name == "" {
		return errors.New("skin file is missing required field 'name'")
	}
	if len(s.Palette) == 0 {
		return errors.New("skin file is missing required field 'palette'")
	}
	for name, hex := range s.Palette {
		if !hexColorPattern.MatchString(hex) {
			return fmt.Errorf("palette[%q]: %q is not a valid 6-digit hex colour", name, hex)
		}
	}
	return nil
}
