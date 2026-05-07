// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// Styles is the compiled, view-facing surface of a skin. Every TUI
// component reads through it (`styles.Table.Cursor`, `styles.Severity
// .Critical`, …) rather than touching the raw skin file — that
// indirection is what makes a theme swap cheap.
type Styles struct {
	Body         BodyStyle
	Header       HeaderStyle
	Frame        FrameStyle
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
// overridden by a more specific role. Logo is the foreground tint
// used for the panel's ASCII logo, mirroring k9s's `body.logoColor`.
type BodyStyle struct {
	Default lipgloss.Style
	Logo    lipgloss.Style
}

// FrameStyle covers the page frame: the border characters around
// the body box and the title strip rendered into the top edge.
// Title is the body title text colour (k9s `frame.title.fgColor`).
// TitleHighlight, TitleCounter and TitleFilter colour the scope
// inside `(...)`, the `[N]` count, and the active filter inside
// the title respectively — see k9s's `NSTitleFmt` /
// `[fg:bg:b]%s([hilite:bg:b]%s…[count:bg:b]%s…]` template.
type FrameStyle struct {
	Border         lipgloss.Style
	Title          lipgloss.Style
	TitleHighlight lipgloss.Style
	TitleCounter   lipgloss.Style
	TitleFilter    lipgloss.Style
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
// active (sorted) header column, regular rows, the cursor row,
// marked rows (Space-selected for bulk actions), and dimmed rows
// (read-only mode + stale data per C2). Note: there is no RowAlt
// — the previous schema had one but no view consumed it; k9s skins
// have no analog and the role was dead code.
type TableStyle struct {
	Header       lipgloss.Style
	HeaderActive lipgloss.Style
	Row          lipgloss.Style
	Cursor       lipgloss.Style
	Marked       lipgloss.Style
	Dimmed       lipgloss.Style
}

// CursorOver returns the cursor style with bg overridden to the
// given colour. Mirrors k9s's runtime recompute of the selected
// style (`internal/ui/select_table.go:128`): k9s ignores the
// static `cursorBgColor` after the first render and uses the
// row's per-row semantic colour (severity for alerts, state for
// silences, StdColor for non-semantic pages) as the selected-row
// bg. Passing nil falls through to the static Cursor — useful as
// a guard when the page has no row colour available.
func (t TableStyle) CursorOver(bg color.Color) lipgloss.Style {
	if bg == nil {
		return t.Cursor
	}
	return t.Cursor.Background(bg)
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

// firstSet returns the first non-empty string in candidates, or ""
// if all are empty. The cascading-fallback chains in compile() are
// just `firstSet(...) → resolve` followed by a body.fg/body.bg floor.
func firstSet(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}

// FgOnly builds a foreground-only Style. The terminal-default
// sentinel skips the Foreground call so the terminal's native fg
// shows through unchanged. Used by the internal compileX helpers
// and exported so chrome / page renderers can construct a
// foreground-only style off a theme role without each owning a
// per-package factory.
func FgOnly(c color.Color) lipgloss.Style {
	s := lipgloss.NewStyle()
	if !isDefaultColor(c) {
		s = s.Foreground(c)
	}
	return s
}

// fgBgStyle builds a fg+bg Style; default sentinels on either axis
// skip the corresponding setter so terminal-native styling carries
// through. This is the engine behind the `-transparent` variants.
func fgBgStyle(fg, bg color.Color) lipgloss.Style {
	s := lipgloss.NewStyle()
	if !isDefaultColor(fg) {
		s = s.Foreground(fg)
	}
	if !isDefaultColor(bg) {
		s = s.Background(bg)
	}
	return s
}

// resolveFgChain parses the first non-empty candidate from chain. If
// all candidates are empty, falls back to body.fgColor — the
// universal floor that compile() guarantees exists. role is woven
// into the parse error so a malformed value points back at the
// role it broke, not just at the colour parser.
func (f *k9sSkinFile) resolveFgChain(role string, chain ...string) (color.Color, error) {
	v := firstSet(chain...)
	if v == "" {
		v = f.K9s.Body.FgColor
	}
	c, err := parseColor(v)
	if err != nil {
		return nil, fmt.Errorf("role %q: %w", role, err)
	}
	return c, nil
}

// resolveBgChain mirrors resolveFgChain with body.bgColor as the
// universal floor.
func (f *k9sSkinFile) resolveBgChain(role string, chain ...string) (color.Color, error) {
	v := firstSet(chain...)
	if v == "" {
		v = f.K9s.Body.BgColor
	}
	c, err := parseColor(v)
	if err != nil {
		return nil, fmt.Errorf("role %q: %w", role, err)
	}
	return c, nil
}

// resolveStatus parses one of the post-fallback frame.status fields.
// applyStockFallback runs before compile, so these are always
// non-empty by the time we get here — but we still error if the
// value fails to parse, which catches a bad SVG name in a user
// skin or a typo in our own stockStatus table.
func (f *k9sSkinFile) resolveStatus(role, value string) (color.Color, error) {
	c, err := parseColor(value)
	if err != nil {
		return nil, fmt.Errorf("role %q: %w", role, err)
	}
	return c, nil
}

// compile resolves a parsed (and stock-filled) skinFile into a
// fully-populated *Styles. The order tracks the role-mapping table
// in docs/design/k9s-skins-dropin.md ("Role mapping (full)") so
// reviewers can read the two side-by-side.
func compile(f *k9sSkinFile) (*Styles, error) {
	out := &Styles{}
	var err error

	if out.Body, err = compileBody(f); err != nil {
		return nil, err
	}
	if out.Header, err = compileHeader(f); err != nil {
		return nil, err
	}
	if out.Frame, err = compileFrame(f); err != nil {
		return nil, err
	}
	if out.Table, err = compileTable(f); err != nil {
		return nil, err
	}
	if out.Severity, err = compileSeverity(f); err != nil {
		return nil, err
	}
	if out.SilenceState, err = compileSilenceState(f); err != nil {
		return nil, err
	}
	if out.Prompt, err = compilePrompt(f); err != nil {
		return nil, err
	}
	if out.Flash, err = compileFlash(f); err != nil {
		return nil, err
	}
	if out.Crumbs, err = compileCrumbs(f); err != nil {
		return nil, err
	}
	if out.Hint, err = compileHint(f); err != nil {
		return nil, err
	}
	if out.Modal, err = compileModal(f); err != nil {
		return nil, err
	}
	if out.YAML, err = compileYAML(f); err != nil {
		return nil, err
	}
	return out, nil
}

func compileBody(f *k9sSkinFile) (BodyStyle, error) {
	fg, err := parseColor(f.K9s.Body.FgColor)
	if err != nil {
		return BodyStyle{}, fmt.Errorf("body.fgColor: %w", err)
	}
	bg, err := parseColor(f.K9s.Body.BgColor)
	if err != nil {
		return BodyStyle{}, fmt.Errorf("body.bgColor: %w", err)
	}
	logo, err := f.resolveFgChain("body.logo", f.K9s.Body.LogoColor)
	if err != nil {
		return BodyStyle{}, err
	}
	return BodyStyle{
		Default: fgBgStyle(fg, bg),
		Logo:    FgOnly(logo),
	}, nil
}

func compileFrame(f *k9sSkinFile) (FrameStyle, error) {
	border, err := f.resolveFgChain("frame.border", f.K9s.Frame.Border.FgColor)
	if err != nil {
		return FrameStyle{}, err
	}
	title, err := f.resolveFgChain("frame.title", f.K9s.Frame.Title.FgColor)
	if err != nil {
		return FrameStyle{}, err
	}
	highlight, err := f.resolveFgChain("frame.title.highlight",
		f.K9s.Frame.Title.HighlightColor)
	if err != nil {
		return FrameStyle{}, err
	}
	counter, err := f.resolveFgChain("frame.title.counter",
		f.K9s.Frame.Title.CounterColor, f.K9s.Frame.Title.HighlightColor)
	if err != nil {
		return FrameStyle{}, err
	}
	filter, err := f.resolveFgChain("frame.title.filter",
		f.K9s.Frame.Title.FilterColor, f.K9s.Frame.Title.HighlightColor)
	if err != nil {
		return FrameStyle{}, err
	}
	return FrameStyle{
		Border:         FgOnly(border),
		Title:          FgOnly(title),
		TitleHighlight: FgOnly(highlight),
		TitleCounter:   FgOnly(counter),
		TitleFilter:    FgOnly(filter),
	}, nil
}

func compileHeader(f *k9sSkinFile) (HeaderStyle, error) {
	fg, err := f.resolveFgChain("header.fg", f.K9s.Frame.Title.FgColor)
	if err != nil {
		return HeaderStyle{}, err
	}
	bg, err := f.resolveBgChain("header.bg", f.K9s.Frame.Title.BgColor)
	if err != nil {
		return HeaderStyle{}, err
	}
	accent, err := f.resolveFgChain("header.accent", f.K9s.Body.LogoColor)
	if err != nil {
		return HeaderStyle{}, err
	}
	ok, err := f.resolveStatus("header.ok", f.K9s.Frame.Status.AddColor)
	if err != nil {
		return HeaderStyle{}, err
	}
	warn, err := f.resolveStatus("header.warn", f.K9s.Frame.Status.HighlightColor)
	if err != nil {
		return HeaderStyle{}, err
	}
	errC, err := f.resolveStatus("header.error", f.K9s.Frame.Status.ErrorColor)
	if err != nil {
		return HeaderStyle{}, err
	}
	return HeaderStyle{
		Default: fgBgStyle(fg, bg),
		Accent:  FgOnly(accent),
		OK:      FgOnly(ok),
		Warn:    FgOnly(warn),
		Error:   FgOnly(errC),
	}, nil
}

func compileTable(f *k9sSkinFile) (TableStyle, error) {
	headerFg, err := f.resolveFgChain("table.header.fg", f.K9s.Views.Table.Header.FgColor)
	if err != nil {
		return TableStyle{}, err
	}
	headerBg, err := f.resolveBgChain("table.header.bg", f.K9s.Views.Table.Header.BgColor)
	if err != nil {
		return TableStyle{}, err
	}
	headerActiveFg, err := f.resolveFgChain("table.header_active.fg",
		f.K9s.Views.Table.Header.SorterColor, f.K9s.Views.Table.Header.FgColor)
	if err != nil {
		return TableStyle{}, err
	}
	rowFg, err := f.resolveFgChain("table.row.fg", f.K9s.Views.Table.FgColor)
	if err != nil {
		return TableStyle{}, err
	}
	rowBg, err := f.resolveBgChain("table.row.bg", f.K9s.Views.Table.BgColor)
	if err != nil {
		return TableStyle{}, err
	}

	// Cursor: when both axes are missing, k9s inverts body. Mirror
	// that runtime default. When only one axis is set, fall back to
	// body for the other (don't invert just one).
	cFg := f.K9s.Views.Table.CursorFgColor
	cBg := f.K9s.Views.Table.CursorBgColor
	if cFg == "" && cBg == "" {
		cFg, cBg = f.K9s.Body.BgColor, f.K9s.Body.FgColor
	} else {
		if cFg == "" {
			cFg = f.K9s.Body.FgColor
		}
		if cBg == "" {
			cBg = f.K9s.Body.BgColor
		}
	}
	cursorFg, err := parseColor(cFg)
	if err != nil {
		return TableStyle{}, fmt.Errorf("table.cursor.fg: %w", err)
	}
	cursorBg, err := parseColor(cBg)
	if err != nil {
		return TableStyle{}, fmt.Errorf("table.cursor.bg: %w", err)
	}

	markedFg, err := f.resolveFgChain("table.marked",
		f.K9s.Views.Table.MarkColor, f.K9s.Frame.Title.HighlightColor)
	if err != nil {
		return TableStyle{}, err
	}
	dimmedFg, err := f.resolveFgChain("table.dimmed",
		f.K9s.Frame.Status.CompletedColor, f.K9s.Frame.Status.KillColor)
	if err != nil {
		return TableStyle{}, err
	}
	bodyBg, err := parseColor(f.K9s.Body.BgColor)
	if err != nil {
		return TableStyle{}, fmt.Errorf("table.marked.bg: %w", err)
	}

	return TableStyle{
		Header:       fgBgStyle(headerFg, headerBg),
		HeaderActive: fgBgStyle(headerActiveFg, headerBg),
		Row:          fgBgStyle(rowFg, rowBg),
		// k9s applies tcell.AttrBold to its selected (cursor) row
		// (internal/ui/table.go:337). Match that for parity so a
		// side-by-side comparison reads identically.
		Cursor: fgBgStyle(cursorFg, cursorBg).Bold(true),
		Marked: fgBgStyle(markedFg, bodyBg),
		Dimmed: fgBgStyle(dimmedFg, bodyBg),
	}, nil
}

func compileSeverity(f *k9sSkinFile) (SeverityStyle, error) {
	critical, err := f.resolveStatus("severity.critical", f.K9s.Frame.Status.ErrorColor)
	if err != nil {
		return SeverityStyle{}, err
	}
	warning, err := f.resolveStatus("severity.warning", f.K9s.Frame.Status.HighlightColor)
	if err != nil {
		return SeverityStyle{}, err
	}
	info, err := f.resolveStatus("severity.info", f.K9s.Frame.Status.NewColor)
	if err != nil {
		return SeverityStyle{}, err
	}
	unknown, err := f.resolveStatus("severity.unknown", f.K9s.Frame.Status.KillColor)
	if err != nil {
		return SeverityStyle{}, err
	}
	return SeverityStyle{
		Critical: FgOnly(critical),
		Warning:  FgOnly(warning),
		Info:     FgOnly(info),
		Unknown:  FgOnly(unknown),
	}, nil
}

func compileSilenceState(f *k9sSkinFile) (SilenceStateStyle, error) {
	active, err := f.resolveStatus("silence_state.active", f.K9s.Frame.Status.AddColor)
	if err != nil {
		return SilenceStateStyle{}, err
	}
	pending, err := f.resolveStatus("silence_state.pending", f.K9s.Frame.Status.HighlightColor)
	if err != nil {
		return SilenceStateStyle{}, err
	}
	expired, err := f.resolveStatus("silence_state.expired", f.K9s.Frame.Status.KillColor)
	if err != nil {
		return SilenceStateStyle{}, err
	}
	return SilenceStateStyle{
		Active:  FgOnly(active),
		Pending: FgOnly(pending),
		Expired: FgOnly(expired),
	}, nil
}

func compilePrompt(f *k9sSkinFile) (PromptStyle, error) {
	fg, err := f.resolveFgChain("prompt.fg", f.K9s.Prompt.FgColor)
	if err != nil {
		return PromptStyle{}, err
	}
	bg, err := f.resolveBgChain("prompt.bg", f.K9s.Prompt.BgColor)
	if err != nil {
		return PromptStyle{}, err
	}
	suggestion, err := f.resolveFgChain("prompt.suggestion", f.K9s.Prompt.SuggestColor)
	if err != nil {
		return PromptStyle{}, err
	}
	return PromptStyle{
		Default:    fgBgStyle(fg, bg),
		Suggestion: FgOnly(suggestion),
	}, nil
}

func compileFlash(f *k9sSkinFile) (FlashStyle, error) {
	success, err := f.resolveStatus("flash.success", f.K9s.Frame.Status.AddColor)
	if err != nil {
		return FlashStyle{}, err
	}
	info, err := f.resolveStatus("flash.info", f.K9s.Frame.Status.NewColor)
	if err != nil {
		return FlashStyle{}, err
	}
	warn, err := f.resolveStatus("flash.warn", f.K9s.Frame.Status.HighlightColor)
	if err != nil {
		return FlashStyle{}, err
	}
	errC, err := f.resolveStatus("flash.error", f.K9s.Frame.Status.ErrorColor)
	if err != nil {
		return FlashStyle{}, err
	}
	return FlashStyle{
		Success: FgOnly(success),
		Info:    FgOnly(info),
		Warn:    FgOnly(warn),
		Error:   FgOnly(errC),
	}, nil
}

func compileCrumbs(f *k9sSkinFile) (CrumbsStyle, error) {
	fg, err := f.resolveFgChain("crumbs.fg", f.K9s.Frame.Crumbs.FgColor)
	if err != nil {
		return CrumbsStyle{}, err
	}
	bg, err := f.resolveBgChain("crumbs.bg", f.K9s.Frame.Crumbs.BgColor)
	if err != nil {
		return CrumbsStyle{}, err
	}
	active, err := f.resolveFgChain("crumbs.active",
		f.K9s.Frame.Crumbs.ActiveColor, f.K9s.Frame.Title.HighlightColor)
	if err != nil {
		return CrumbsStyle{}, err
	}
	return CrumbsStyle{
		Default: fgBgStyle(fg, bg),
		Active:  FgOnly(active),
	}, nil
}

func compileHint(f *k9sSkinFile) (HintStyle, error) {
	// k9s `frame.menu` has no bgColor; the strip paints over body.bg.
	fg, err := f.resolveFgChain("hint.fg", f.K9s.Frame.Menu.FgColor)
	if err != nil {
		return HintStyle{}, err
	}
	bg, err := parseColor(f.K9s.Body.BgColor)
	if err != nil {
		return HintStyle{}, fmt.Errorf("hint.bg: %w", err)
	}
	keyChain := firstSet(f.K9s.Frame.Menu.KeyColor)
	if keyChain == "" {
		keyChain = f.K9s.Body.FgColor
	}
	keyC, err := parseColor(keyChain)
	if err != nil {
		return HintStyle{}, fmt.Errorf("hint.key: %w", err)
	}
	helpC, err := f.resolveFgChain("hint.help_key",
		f.K9s.Frame.Menu.NumKeyColor, f.K9s.Frame.Menu.KeyColor)
	if err != nil {
		return HintStyle{}, err
	}
	return HintStyle{
		Default: fgBgStyle(fg, bg),
		Key:     FgOnly(keyC),
		HelpKey: FgOnly(helpC),
	}, nil
}

func compileModal(f *k9sSkinFile) (ModalStyle, error) {
	fg, err := f.resolveFgChain("modal.fg", f.K9s.Dialog.FgColor)
	if err != nil {
		return ModalStyle{}, err
	}
	bg, err := f.resolveBgChain("modal.bg", f.K9s.Dialog.BgColor)
	if err != nil {
		return ModalStyle{}, err
	}
	border, err := f.resolveFgChain("modal.border", f.K9s.Frame.Border.FgColor)
	if err != nil {
		return ModalStyle{}, err
	}
	return ModalStyle{
		Default: fgBgStyle(fg, bg),
		Border:  FgOnly(border),
	}, nil
}

func compileYAML(f *k9sSkinFile) (YAMLStyle, error) {
	key, err := f.resolveFgChain("yaml.key", f.K9s.Views.YAML.KeyColor)
	if err != nil {
		return YAMLStyle{}, err
	}
	value, err := f.resolveFgChain("yaml.value", f.K9s.Views.YAML.ValueColor)
	if err != nil {
		return YAMLStyle{}, err
	}
	punct, err := f.resolveFgChain("yaml.punct", f.K9s.Views.YAML.ColonColor)
	if err != nil {
		return YAMLStyle{}, err
	}
	return YAMLStyle{
		Key:   FgOnly(key),
		Value: FgOnly(value),
		Punct: FgOnly(punct),
	}, nil
}
