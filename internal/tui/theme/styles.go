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
	// DefaultFg is the precomputed fg-only sibling of Default — the
	// panel info column reads it on every frame to colour values
	// without painting the body bg behind them.
	DefaultFg lipgloss.Style
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
	// *Bold variants are pre-computed Bold(true) siblings so the panel
	// title hot path doesn't reconstruct a new lipgloss.Style on every
	// frame. Identical to the corresponding plain field with bold set.
	TitleBold          lipgloss.Style
	TitleHighlightBold lipgloss.Style
	TitleCounterBold   lipgloss.Style
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
	// DefaultFg is the precomputed fg-only sibling of Default — the
	// header chrome reads it on every frame.
	DefaultFg lipgloss.Style
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
	// *Fg variants are foreground-only siblings of the parent style.
	// Pages that paint cells without an inherited bg (header rows
	// rendered inside the column-header line, marked / dimmed cells
	// stacked over a row-level cursor wrap) read these instead of
	// reconstructing a fresh FgOnly Style on every frame. See F12.
	HeaderFg       lipgloss.Style
	HeaderActiveFg lipgloss.Style
	MarkedFg       lipgloss.Style
	DimmedFg       lipgloss.Style
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
	// DefaultFgBold is the fg-only bold sibling of Default. The
	// prompt main glyph reads it on every keystroke; the surrounding
	// chrome is unstyled so painting Default's bg would draw a stripe.
	DefaultFgBold lipgloss.Style
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
	// DefaultBold and ActivePill are precomputed siblings the
	// breadcrumb renderer reads on every frame; ActivePill is the
	// composite "default fg over active bg, bold" pill that decorates
	// the top-of-stack crumb (see footer.Crumbs.Render). Baked at
	// load to avoid rebuilding the same lipgloss.Style on every
	// page push.
	DefaultBold lipgloss.Style
	ActivePill  lipgloss.Style
}

// HintStyle drives the J1 right-zone keybinding hint strip. Key
// highlights the shortcut letter; HelpKey is the always-on `?`
// indicator.
type HintStyle struct {
	Default lipgloss.Style
	Key     lipgloss.Style
	HelpKey lipgloss.Style
	// DefaultFg + DefaultFgBold are precomputed fg-only siblings of
	// Default — the panel hint and tenant strips read them on every
	// frame instead of reconstructing FgOnly + Bold from scratch.
	DefaultFg     lipgloss.Style
	DefaultFgBold lipgloss.Style
	// KeyBold / HelpKeyBold mirror their plain counterparts with
	// Bold(true) baked in — same hot-path rationale.
	KeyBold     lipgloss.Style
	HelpKeyBold lipgloss.Style
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
// fully-populated *Styles. The order tracks the per-role fallback
// chains established at the top of this file so reviewers can read
// the body and the role declarations side-by-side.
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

// styleGather collects color-resolution errors so a compileX
// function can read as a flat sequence of role assignments rather
// than a per-row if-err-return ladder. The first error sticks;
// subsequent calls short-circuit and return a nil color.Color.
// Each compileX checks g.err once before assembling its result.
type styleGather struct {
	f   *k9sSkinFile
	err error
}

// fg resolves a foreground role through the fgChain machinery.
// First error sticks; on a sticky error the call returns nil.
func (g *styleGather) fg(role string, candidates ...string) color.Color {
	if g.err != nil {
		return nil
	}
	c, err := g.f.resolveFgChain(role, candidates...)
	if err != nil {
		g.err = err
	}
	return c
}

// bg is the background-chain analogue of fg.
func (g *styleGather) bg(role string, candidates ...string) color.Color {
	if g.err != nil {
		return nil
	}
	c, err := g.f.resolveBgChain(role, candidates...)
	if err != nil {
		g.err = err
	}
	return c
}

// raw parses a literal color string and prefixes any parse error
// with the role label. Used for slots that bypass the chain
// machinery — body.{fg,bg}Color (the universal floor) and
// table.cursor.{fg,bg} (the body-inversion intercept).
func (g *styleGather) raw(role, value string) color.Color {
	if g.err != nil {
		return nil
	}
	c, err := parseColor(value)
	if err != nil {
		g.err = fmt.Errorf("%s: %w", role, err)
	}
	return c
}

// status resolves a role through resolveStatus (the variant that
// applies the k9s status-color sentinel handling).
func (g *styleGather) status(role, value string) color.Color {
	if g.err != nil {
		return nil
	}
	c, err := g.f.resolveStatus(role, value)
	if err != nil {
		g.err = err
	}
	return c
}

func compileBody(f *k9sSkinFile) (BodyStyle, error) {
	g := &styleGather{f: f}
	fg := g.raw("body.fgColor", f.K9s.Body.FgColor)
	bg := g.raw("body.bgColor", f.K9s.Body.BgColor)
	logo := g.fg("body.logo", f.K9s.Body.LogoColor)
	if g.err != nil {
		return BodyStyle{}, g.err
	}
	return BodyStyle{
		Default:   fgBgStyle(fg, bg),
		Logo:      FgOnly(logo),
		DefaultFg: FgOnly(fg),
	}, nil
}

func compileFrame(f *k9sSkinFile) (FrameStyle, error) {
	g := &styleGather{f: f}
	border := g.fg("frame.border", f.K9s.Frame.Border.FgColor)
	title := g.fg("frame.title", f.K9s.Frame.Title.FgColor)
	highlight := g.fg("frame.title.highlight", f.K9s.Frame.Title.HighlightColor)
	counter := g.fg("frame.title.counter",
		f.K9s.Frame.Title.CounterColor, f.K9s.Frame.Title.HighlightColor)
	filter := g.fg("frame.title.filter",
		f.K9s.Frame.Title.FilterColor, f.K9s.Frame.Title.HighlightColor)
	if g.err != nil {
		return FrameStyle{}, g.err
	}
	titleStyle := FgOnly(title)
	titleHighlight := FgOnly(highlight)
	titleCounter := FgOnly(counter)
	return FrameStyle{
		Border:             FgOnly(border),
		Title:              titleStyle,
		TitleHighlight:     titleHighlight,
		TitleCounter:       titleCounter,
		TitleFilter:        FgOnly(filter),
		TitleBold:          titleStyle.Bold(true),
		TitleHighlightBold: titleHighlight.Bold(true),
		TitleCounterBold:   titleCounter.Bold(true),
	}, nil
}

func compileHeader(f *k9sSkinFile) (HeaderStyle, error) {
	g := &styleGather{f: f}
	fg := g.fg("header.fg", f.K9s.Frame.Title.FgColor)
	bg := g.bg("header.bg", f.K9s.Frame.Title.BgColor)
	accent := g.fg("header.accent", f.K9s.Body.LogoColor)
	ok := g.status("header.ok", f.K9s.Frame.Status.AddColor)
	warn := g.status("header.warn", f.K9s.Frame.Status.HighlightColor)
	errC := g.status("header.error", f.K9s.Frame.Status.ErrorColor)
	if g.err != nil {
		return HeaderStyle{}, g.err
	}
	return HeaderStyle{
		Default:   fgBgStyle(fg, bg),
		Accent:    FgOnly(accent),
		OK:        FgOnly(ok),
		Warn:      FgOnly(warn),
		Error:     FgOnly(errC),
		DefaultFg: FgOnly(fg),
	}, nil
}

func compileTable(f *k9sSkinFile) (TableStyle, error) {
	g := &styleGather{f: f}
	headerFg := g.fg("table.header.fg", f.K9s.Views.Table.Header.FgColor)
	headerBg := g.bg("table.header.bg", f.K9s.Views.Table.Header.BgColor)
	headerActiveFg := g.fg("table.header_active.fg",
		f.K9s.Views.Table.Header.SorterColor, f.K9s.Views.Table.Header.FgColor)
	rowFg := g.fg("table.row.fg", f.K9s.Views.Table.FgColor)
	rowBg := g.bg("table.row.bg", f.K9s.Views.Table.BgColor)

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
	cursorFg := g.raw("table.cursor.fg", cFg)
	cursorBg := g.raw("table.cursor.bg", cBg)

	markedFg := g.fg("table.marked",
		f.K9s.Views.Table.MarkColor, f.K9s.Frame.Title.HighlightColor)
	dimmedFg := g.fg("table.dimmed",
		f.K9s.Frame.Status.CompletedColor, f.K9s.Frame.Status.KillColor)
	bodyBg := g.raw("table.marked.bg", f.K9s.Body.BgColor)

	if g.err != nil {
		return TableStyle{}, g.err
	}
	return TableStyle{
		Header:       fgBgStyle(headerFg, headerBg),
		HeaderActive: fgBgStyle(headerActiveFg, headerBg),
		Row:          fgBgStyle(rowFg, rowBg),
		// k9s applies tcell.AttrBold to its selected (cursor) row
		// (internal/ui/table.go:337). Match that for parity so a
		// side-by-side comparison reads identically.
		Cursor:         fgBgStyle(cursorFg, cursorBg).Bold(true),
		Marked:         fgBgStyle(markedFg, bodyBg),
		Dimmed:         fgBgStyle(dimmedFg, bodyBg),
		HeaderFg:       FgOnly(headerFg),
		HeaderActiveFg: FgOnly(headerActiveFg),
		MarkedFg:       FgOnly(markedFg),
		DimmedFg:       FgOnly(dimmedFg),
	}, nil
}

func compileSeverity(f *k9sSkinFile) (SeverityStyle, error) {
	g := &styleGather{f: f}
	critical := g.status("severity.critical", f.K9s.Frame.Status.ErrorColor)
	warning := g.status("severity.warning", f.K9s.Frame.Status.HighlightColor)
	info := g.status("severity.info", f.K9s.Frame.Status.NewColor)
	unknown := g.status("severity.unknown", f.K9s.Frame.Status.KillColor)
	if g.err != nil {
		return SeverityStyle{}, g.err
	}
	return SeverityStyle{
		Critical: FgOnly(critical),
		Warning:  FgOnly(warning),
		Info:     FgOnly(info),
		Unknown:  FgOnly(unknown),
	}, nil
}

func compileSilenceState(f *k9sSkinFile) (SilenceStateStyle, error) {
	g := &styleGather{f: f}
	active := g.status("silence_state.active", f.K9s.Frame.Status.AddColor)
	pending := g.status("silence_state.pending", f.K9s.Frame.Status.HighlightColor)
	expired := g.status("silence_state.expired", f.K9s.Frame.Status.KillColor)
	if g.err != nil {
		return SilenceStateStyle{}, g.err
	}
	return SilenceStateStyle{
		Active:  FgOnly(active),
		Pending: FgOnly(pending),
		Expired: FgOnly(expired),
	}, nil
}

func compilePrompt(f *k9sSkinFile) (PromptStyle, error) {
	g := &styleGather{f: f}
	fg := g.fg("prompt.fg", f.K9s.Prompt.FgColor)
	bg := g.bg("prompt.bg", f.K9s.Prompt.BgColor)
	suggestion := g.fg("prompt.suggestion", f.K9s.Prompt.SuggestColor)
	if g.err != nil {
		return PromptStyle{}, g.err
	}
	return PromptStyle{
		Default:       fgBgStyle(fg, bg),
		Suggestion:    FgOnly(suggestion),
		DefaultFgBold: FgOnly(fg).Bold(true),
	}, nil
}

func compileFlash(f *k9sSkinFile) (FlashStyle, error) {
	g := &styleGather{f: f}
	success := g.status("flash.success", f.K9s.Frame.Status.AddColor)
	info := g.status("flash.info", f.K9s.Frame.Status.NewColor)
	warn := g.status("flash.warn", f.K9s.Frame.Status.HighlightColor)
	errC := g.status("flash.error", f.K9s.Frame.Status.ErrorColor)
	if g.err != nil {
		return FlashStyle{}, g.err
	}
	return FlashStyle{
		Success: FgOnly(success),
		Info:    FgOnly(info),
		Warn:    FgOnly(warn),
		Error:   FgOnly(errC),
	}, nil
}

func compileCrumbs(f *k9sSkinFile) (CrumbsStyle, error) {
	g := &styleGather{f: f}
	fg := g.fg("crumbs.fg", f.K9s.Frame.Crumbs.FgColor)
	bg := g.bg("crumbs.bg", f.K9s.Frame.Crumbs.BgColor)
	active := g.fg("crumbs.active",
		f.K9s.Frame.Crumbs.ActiveColor, f.K9s.Frame.Title.HighlightColor)
	if g.err != nil {
		return CrumbsStyle{}, g.err
	}
	defaultStyle := fgBgStyle(fg, bg)
	activeStyle := FgOnly(active)
	return CrumbsStyle{
		Default:     defaultStyle,
		Active:      activeStyle,
		DefaultBold: defaultStyle.Bold(true),
		ActivePill: lipgloss.NewStyle().
			Foreground(defaultStyle.GetForeground()).
			Background(activeStyle.GetForeground()).
			Bold(true),
	}, nil
}

func compileHint(f *k9sSkinFile) (HintStyle, error) {
	g := &styleGather{f: f}
	// k9s `frame.menu` has no bgColor; the strip paints over body.bg.
	fg := g.fg("hint.fg", f.K9s.Frame.Menu.FgColor)
	bg := g.raw("hint.bg", f.K9s.Body.BgColor)
	// hint.key falls back to body.fg directly via firstSet — the
	// chain machinery would consult body.fgColor as the floor too,
	// but the explicit firstSet here keeps the intent obvious in
	// the role schema.
	keyChain := firstSet(f.K9s.Frame.Menu.KeyColor)
	if keyChain == "" {
		keyChain = f.K9s.Body.FgColor
	}
	keyC := g.raw("hint.key", keyChain)
	helpC := g.fg("hint.help_key",
		f.K9s.Frame.Menu.NumKeyColor, f.K9s.Frame.Menu.KeyColor)
	if g.err != nil {
		return HintStyle{}, g.err
	}
	defaultFg := FgOnly(fg)
	keyStyle := FgOnly(keyC)
	helpStyle := FgOnly(helpC)
	return HintStyle{
		Default:       fgBgStyle(fg, bg),
		Key:           keyStyle,
		HelpKey:       helpStyle,
		DefaultFg:     defaultFg,
		DefaultFgBold: defaultFg.Bold(true),
		KeyBold:       keyStyle.Bold(true),
		HelpKeyBold:   helpStyle.Bold(true),
	}, nil
}

func compileModal(f *k9sSkinFile) (ModalStyle, error) {
	g := &styleGather{f: f}
	fg := g.fg("modal.fg", f.K9s.Dialog.FgColor)
	bg := g.bg("modal.bg", f.K9s.Dialog.BgColor)
	border := g.fg("modal.border", f.K9s.Frame.Border.FgColor)
	if g.err != nil {
		return ModalStyle{}, g.err
	}
	return ModalStyle{
		Default: fgBgStyle(fg, bg),
		Border:  FgOnly(border),
	}, nil
}

func compileYAML(f *k9sSkinFile) (YAMLStyle, error) {
	g := &styleGather{f: f}
	key := g.fg("yaml.key", f.K9s.Views.YAML.KeyColor)
	value := g.fg("yaml.value", f.K9s.Views.YAML.ValueColor)
	punct := g.fg("yaml.punct", f.K9s.Views.YAML.ColonColor)
	if g.err != nil {
		return YAMLStyle{}, g.err
	}
	return YAMLStyle{
		Key:   FgOnly(key),
		Value: FgOnly(value),
		Punct: FgOnly(punct),
	}, nil
}
