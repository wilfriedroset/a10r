// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// Styles is the compiled, view-facing surface of a skin. Every TUI
// component reads through it (`styles.Table.Cursor`, `styles.Severity
// .Critical`, …) rather than touching the raw palette — that
// indirection is what makes a theme swap cheap.
type Styles struct {
	Body         BodyStyle
	Header       HeaderStyle
	Table        TableStyle
	Severity     SeverityStyle
	SilenceState SilenceStateStyle
	Prompt       PromptStyle
	Flash        FlashStyle
	Crumbs       CrumbsStyle
	Hint         HintStyle
	Modal        ModalStyle
	YAML         YAMLStyle
}

// BodyStyle covers the default body fg/bg used everywhere not
// overridden by a more specific role.
type BodyStyle struct {
	Default lipgloss.Style
}

// HeaderStyle drives the J1 three-zone header: Default carries the
// fg+bg, Accent/OK/Warn/Error are foreground-only colours used for
// the tenant indicator, connection state badges, and counts.
type HeaderStyle struct {
	Default lipgloss.Style
	Accent  lipgloss.Style
	OK      lipgloss.Style
	Warn    lipgloss.Style
	Error   lipgloss.Style
}

// TableStyle covers every table-row state: the column header, the
// active (sorted) header column, regular rows, alternating-stripe
// rows, the cursor row, marked rows (Space-selected for bulk
// actions), and dimmed rows (read-only mode + stale data per C2).
type TableStyle struct {
	Header       lipgloss.Style
	HeaderActive lipgloss.Style
	Row          lipgloss.Style
	RowAlt       lipgloss.Style
	Cursor       lipgloss.Style
	Marked       lipgloss.Style
	Dimmed       lipgloss.Style
}

// SeverityStyle colours alert rows by their severity label.
// Foreground-only — the row's bg comes from TableStyle.
type SeverityStyle struct {
	Critical lipgloss.Style
	Warning  lipgloss.Style
	Info     lipgloss.Style
	Unknown  lipgloss.Style
}

// SilenceStateStyle colours silence rows by status.state.
type SilenceStateStyle struct {
	Active  lipgloss.Style
	Pending lipgloss.Style
	Expired lipgloss.Style
}

// PromptStyle drives the bottom-strip `:` and `/` prompts.
type PromptStyle struct {
	Default    lipgloss.Style
	Suggestion lipgloss.Style
}

// FlashStyle colours the bottom-strip ephemeral messages by level.
type FlashStyle struct {
	Success lipgloss.Style
	Info    lipgloss.Style
	Warn    lipgloss.Style
	Error   lipgloss.Style
}

// CrumbsStyle drives the breadcrumb strip; Active highlights the
// current top-of-stack crumb.
type CrumbsStyle struct {
	Default lipgloss.Style
	Active  lipgloss.Style
}

// HintStyle drives the J1 right-zone keybinding hint strip. Key
// highlights the shortcut letter; HelpKey is the always-on `?`
// indicator.
type HintStyle struct {
	Default lipgloss.Style
	Key     lipgloss.Style
	HelpKey lipgloss.Style
}

// ModalStyle covers the confirm-dialog / picker / help overlays.
type ModalStyle struct {
	Default lipgloss.Style
	Border  lipgloss.Style
}

// YAMLStyle colours the status pane's raw config.original viewer
// (per I1) and, post-v0.1, the Mimir config editor.
type YAMLStyle struct {
	Key   lipgloss.Style
	Value lipgloss.Style
	Punct lipgloss.Style
}

// compile resolves a parsed skinFile into a fully-populated *Styles.
// Palette refs that cannot be resolved surface as a structured error
// naming the role and the offending palette key.
func compile(s skinFile) (*Styles, error) {
	out := &Styles{}
	var err error

	if out.Body, err = compileBody(s); err != nil {
		return nil, err
	}
	if out.Header, err = compileHeader(s); err != nil {
		return nil, err
	}
	if out.Table, err = compileTable(s); err != nil {
		return nil, err
	}
	if out.Severity, err = compileSeverity(s); err != nil {
		return nil, err
	}
	if out.SilenceState, err = compileSilenceState(s); err != nil {
		return nil, err
	}
	if out.Prompt, err = compilePrompt(s); err != nil {
		return nil, err
	}
	if out.Flash, err = compileFlash(s); err != nil {
		return nil, err
	}
	if out.Crumbs, err = compileCrumbs(s); err != nil {
		return nil, err
	}
	if out.Hint, err = compileHint(s); err != nil {
		return nil, err
	}
	if out.Modal, err = compileModal(s); err != nil {
		return nil, err
	}
	if out.YAML, err = compileYAML(s); err != nil {
		return nil, err
	}
	return out, nil
}

// resolve looks up a palette ref. Returns an error naming the role
// and the offending key so the user can fix the right field.
func resolve(palette map[string]string, role, key string) (color.Color, error) {
	if key == "" {
		return nil, fmt.Errorf("role %q: missing palette ref", role)
	}
	hex, ok := palette[key]
	if !ok {
		return nil, fmt.Errorf("role %q: palette key %q is not defined", role, key)
	}
	return lipgloss.Color(hex), nil
}

// fgbg builds a Style with both foreground and background. fgOnly
// builds a foreground-only Style — the caller's enclosing widget
// supplies the background.
func fgbg(palette map[string]string, role, fg, bg string) (lipgloss.Style, error) {
	f, err := resolve(palette, role, fg)
	if err != nil {
		return lipgloss.NewStyle(), err
	}
	b, err := resolve(palette, role, bg)
	if err != nil {
		return lipgloss.NewStyle(), err
	}
	return lipgloss.NewStyle().Foreground(f).Background(b), nil
}

func fgOnly(palette map[string]string, role, fg string) (lipgloss.Style, error) {
	f, err := resolve(palette, role, fg)
	if err != nil {
		return lipgloss.NewStyle(), err
	}
	return lipgloss.NewStyle().Foreground(f), nil
}

func compileBody(s skinFile) (BodyStyle, error) {
	style, err := fgbg(s.Palette, "body", s.Roles.Body.Fg, s.Roles.Body.Bg)
	if err != nil {
		return BodyStyle{}, err
	}
	return BodyStyle{Default: style}, nil
}

func compileHeader(s skinFile) (HeaderStyle, error) {
	r := s.Roles.Header
	def, err := fgbg(s.Palette, "header", r.Fg, r.Bg)
	if err != nil {
		return HeaderStyle{}, err
	}
	accent, err := fgOnly(s.Palette, "header.accent", r.Accent)
	if err != nil {
		return HeaderStyle{}, err
	}
	ok, err := fgOnly(s.Palette, "header.ok", r.OK)
	if err != nil {
		return HeaderStyle{}, err
	}
	warn, err := fgOnly(s.Palette, "header.warn", r.Warn)
	if err != nil {
		return HeaderStyle{}, err
	}
	errStyle, err := fgOnly(s.Palette, "header.error", r.Error)
	if err != nil {
		return HeaderStyle{}, err
	}
	return HeaderStyle{Default: def, Accent: accent, OK: ok, Warn: warn, Error: errStyle}, nil
}

func compileTable(s skinFile) (TableStyle, error) {
	r := s.Roles.Table
	header, err := fgbg(s.Palette, "table.header", r.Header.Fg, r.Header.Bg)
	if err != nil {
		return TableStyle{}, err
	}
	headerActive, err := fgbg(s.Palette, "table.header_active", r.HeaderActive.Fg, r.HeaderActive.Bg)
	if err != nil {
		return TableStyle{}, err
	}
	row, err := fgbg(s.Palette, "table.row", r.Row.Fg, r.Row.Bg)
	if err != nil {
		return TableStyle{}, err
	}
	rowAlt, err := fgbg(s.Palette, "table.row_alt", r.RowAlt.Fg, r.RowAlt.Bg)
	if err != nil {
		return TableStyle{}, err
	}
	cursor, err := fgbg(s.Palette, "table.cursor", r.Cursor.Fg, r.Cursor.Bg)
	if err != nil {
		return TableStyle{}, err
	}
	marked, err := fgbg(s.Palette, "table.marked", r.Marked.Fg, r.Marked.Bg)
	if err != nil {
		return TableStyle{}, err
	}
	dimmed, err := fgbg(s.Palette, "table.dimmed", r.Dimmed.Fg, r.Dimmed.Bg)
	if err != nil {
		return TableStyle{}, err
	}
	return TableStyle{
		Header: header, HeaderActive: headerActive,
		Row: row, RowAlt: rowAlt,
		Cursor: cursor, Marked: marked, Dimmed: dimmed,
	}, nil
}

func compileSeverity(s skinFile) (SeverityStyle, error) {
	r := s.Roles.Severity
	critical, err := fgOnly(s.Palette, "severity.critical", r.Critical)
	if err != nil {
		return SeverityStyle{}, err
	}
	warning, err := fgOnly(s.Palette, "severity.warning", r.Warning)
	if err != nil {
		return SeverityStyle{}, err
	}
	info, err := fgOnly(s.Palette, "severity.info", r.Info)
	if err != nil {
		return SeverityStyle{}, err
	}
	unknown, err := fgOnly(s.Palette, "severity.unknown", r.Unknown)
	if err != nil {
		return SeverityStyle{}, err
	}
	return SeverityStyle{Critical: critical, Warning: warning, Info: info, Unknown: unknown}, nil
}

func compileSilenceState(s skinFile) (SilenceStateStyle, error) {
	r := s.Roles.SilenceState
	active, err := fgOnly(s.Palette, "silence_state.active", r.Active)
	if err != nil {
		return SilenceStateStyle{}, err
	}
	pending, err := fgOnly(s.Palette, "silence_state.pending", r.Pending)
	if err != nil {
		return SilenceStateStyle{}, err
	}
	expired, err := fgOnly(s.Palette, "silence_state.expired", r.Expired)
	if err != nil {
		return SilenceStateStyle{}, err
	}
	return SilenceStateStyle{Active: active, Pending: pending, Expired: expired}, nil
}

func compilePrompt(s skinFile) (PromptStyle, error) {
	r := s.Roles.Prompt
	def, err := fgbg(s.Palette, "prompt", r.Fg, r.Bg)
	if err != nil {
		return PromptStyle{}, err
	}
	suggestion, err := fgOnly(s.Palette, "prompt.suggestion", r.Suggestion)
	if err != nil {
		return PromptStyle{}, err
	}
	return PromptStyle{Default: def, Suggestion: suggestion}, nil
}

func compileFlash(s skinFile) (FlashStyle, error) {
	r := s.Roles.Flash
	success, err := fgOnly(s.Palette, "flash.success", r.Success)
	if err != nil {
		return FlashStyle{}, err
	}
	info, err := fgOnly(s.Palette, "flash.info", r.Info)
	if err != nil {
		return FlashStyle{}, err
	}
	warn, err := fgOnly(s.Palette, "flash.warn", r.Warn)
	if err != nil {
		return FlashStyle{}, err
	}
	errStyle, err := fgOnly(s.Palette, "flash.error", r.Error)
	if err != nil {
		return FlashStyle{}, err
	}
	return FlashStyle{Success: success, Info: info, Warn: warn, Error: errStyle}, nil
}

func compileCrumbs(s skinFile) (CrumbsStyle, error) {
	r := s.Roles.Crumbs
	def, err := fgbg(s.Palette, "crumbs", r.Fg, r.Bg)
	if err != nil {
		return CrumbsStyle{}, err
	}
	active, err := fgOnly(s.Palette, "crumbs.active", r.Active)
	if err != nil {
		return CrumbsStyle{}, err
	}
	return CrumbsStyle{Default: def, Active: active}, nil
}

func compileHint(s skinFile) (HintStyle, error) {
	r := s.Roles.Hint
	def, err := fgbg(s.Palette, "hint", r.Fg, r.Bg)
	if err != nil {
		return HintStyle{}, err
	}
	key, err := fgOnly(s.Palette, "hint.key", r.Key)
	if err != nil {
		return HintStyle{}, err
	}
	helpKey, err := fgOnly(s.Palette, "hint.help_key", r.HelpKey)
	if err != nil {
		return HintStyle{}, err
	}
	return HintStyle{Default: def, Key: key, HelpKey: helpKey}, nil
}

func compileModal(s skinFile) (ModalStyle, error) {
	r := s.Roles.Modal
	def, err := fgbg(s.Palette, "modal", r.Fg, r.Bg)
	if err != nil {
		return ModalStyle{}, err
	}
	border, err := fgOnly(s.Palette, "modal.border", r.Border)
	if err != nil {
		return ModalStyle{}, err
	}
	return ModalStyle{Default: def, Border: border}, nil
}

func compileYAML(s skinFile) (YAMLStyle, error) {
	r := s.Roles.YAML
	key, err := fgOnly(s.Palette, "yaml.key", r.Key)
	if err != nil {
		return YAMLStyle{}, err
	}
	value, err := fgOnly(s.Palette, "yaml.value", r.Value)
	if err != nil {
		return YAMLStyle{}, err
	}
	punct, err := fgOnly(s.Palette, "yaml.punct", r.Punct)
	if err != nil {
		return YAMLStyle{}, err
	}
	return YAMLStyle{Key: key, Value: value, Punct: punct}, nil
}
