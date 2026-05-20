// SPDX-License-Identifier: Apache-2.0

// Package theme parses k9s-format skin files and compiles them into
// a Styles struct of lipgloss.Style values the rest of the TUI
// consumes by role name (e.g. styles.Table.Cursor).
//
// a10r consumes k9s skins drop-in: any skin from derailed/k9s or the
// community works without conversion, including those that use the
// `default` keyword for terminal-native bg or SVG color names. The
// per-role fallback chains in styles.go are the source of truth for
// the schema mapping; the comment at the top of compile() lists
// them in the order they fire.
package theme

import "errors"

// k9sSkinFile mirrors the subset of the k9s skin YAML schema that
// a10r consumes. We deliberately model only the fields we read;
// YAML decoding is permissive (KnownFields(false)) so upstream
// additions like `views.charts.*`, `info.cpuColor`, `dialog.button*`
// etc. are silently ignored. The permissive choice is what lets
// arbitrary k9s skins land drop-in.
//
// Empty-string semantics: the YAML decoder leaves absent string
// fields as "". Every cascading resolver in styles.go treats "" as
// "field not set, try the next link in the chain". No skin uses ""
// as an intentional value.
type k9sSkinFile struct {
	K9s k9sBlock `yaml:"k9s"`
}

type k9sBlock struct {
	Body   k9sBody   `yaml:"body"`
	Prompt k9sPrompt `yaml:"prompt"`
	Frame  k9sFrame  `yaml:"frame"`
	Views  k9sViews  `yaml:"views"`
	Dialog k9sDialog `yaml:"dialog"`
}

type k9sBody struct {
	FgColor   string `yaml:"fgColor"`
	BgColor   string `yaml:"bgColor"`
	LogoColor string `yaml:"logoColor"`
}

type k9sPrompt struct {
	FgColor      string `yaml:"fgColor"`
	BgColor      string `yaml:"bgColor"`
	SuggestColor string `yaml:"suggestColor"`
}

type k9sFrame struct {
	Title  k9sFrameTitle  `yaml:"title"`
	Border k9sFrameBorder `yaml:"border"`
	Menu   k9sFrameMenu   `yaml:"menu"`
	Crumbs k9sFrameCrumbs `yaml:"crumbs"`
	Status k9sFrameStatus `yaml:"status"`
}

type k9sFrameTitle struct {
	FgColor        string `yaml:"fgColor"`
	BgColor        string `yaml:"bgColor"`
	HighlightColor string `yaml:"highlightColor"`
	CounterColor   string `yaml:"counterColor"`
	FilterColor    string `yaml:"filterColor"`
}

type k9sFrameBorder struct {
	FgColor    string `yaml:"fgColor"`
	FocusColor string `yaml:"focusColor"`
}

type k9sFrameMenu struct {
	FgColor     string `yaml:"fgColor"`
	KeyColor    string `yaml:"keyColor"`
	NumKeyColor string `yaml:"numKeyColor"`
}

type k9sFrameCrumbs struct {
	FgColor     string `yaml:"fgColor"`
	BgColor     string `yaml:"bgColor"`
	ActiveColor string `yaml:"activeColor"`
}

// k9sFrameStatus is the load-bearing block: severity, silence_state
// and flash colors all derive from it. If a field is empty after
// parse, applyStockFallback fills it from the k9s upstream stock
// skin so the compile step always sees real colors.
type k9sFrameStatus struct {
	NewColor       string `yaml:"newColor"`
	ModifyColor    string `yaml:"modifyColor"`
	AddColor       string `yaml:"addColor"`
	ErrorColor     string `yaml:"errorColor"`
	HighlightColor string `yaml:"highlightColor"`
	KillColor      string `yaml:"killColor"`
	CompletedColor string `yaml:"completedColor"`
}

type k9sViews struct {
	Table k9sViewsTable `yaml:"table"`
	YAML  k9sViewsYAML  `yaml:"yaml"`
}

type k9sViewsTable struct {
	FgColor       string              `yaml:"fgColor"`
	BgColor       string              `yaml:"bgColor"`
	CursorFgColor string              `yaml:"cursorFgColor"`
	CursorBgColor string              `yaml:"cursorBgColor"`
	MarkColor     string              `yaml:"markColor"`
	Header        k9sViewsTableHeader `yaml:"header"`
}

type k9sViewsTableHeader struct {
	FgColor     string `yaml:"fgColor"`
	BgColor     string `yaml:"bgColor"`
	SorterColor string `yaml:"sorterColor"`
}

type k9sViewsYAML struct {
	KeyColor   string `yaml:"keyColor"`
	ValueColor string `yaml:"valueColor"`
	ColonColor string `yaml:"colonColor"`
}

type k9sDialog struct {
	FgColor string `yaml:"fgColor"`
	BgColor string `yaml:"bgColor"`
}

// validate enforces the strictly-required fields: only
// `body.fgColor` and `body.bgColor` are mandatory. frame.status.*
// are soft-required and patched in by applyStockFallback before
// compile runs.
func (f *k9sSkinFile) validate() error {
	if f.K9s.Body.FgColor == "" {
		return errors.New("required field k9s.body.fgColor is missing")
	}
	if f.K9s.Body.BgColor == "" {
		return errors.New("required field k9s.body.bgColor is missing")
	}
	return nil
}

// applyStockFallback fills the five severity-driving status fields
// from the k9s upstream stock skin when the user's skin omits them.
// The unconditional final fallback to body.fg in compile() handles
// the truly-pathological case where stock somehow doesn't help, but
// in practice every catppuccin/community skin we sampled either
// sets these explicitly or accepts the stock defaults.
func (f *k9sSkinFile) applyStockFallback() {
	s := &f.K9s.Frame.Status
	if s.NewColor == "" {
		s.NewColor = stockStatus.NewColor
	}
	if s.ErrorColor == "" {
		s.ErrorColor = stockStatus.ErrorColor
	}
	if s.AddColor == "" {
		s.AddColor = stockStatus.AddColor
	}
	if s.KillColor == "" {
		s.KillColor = stockStatus.KillColor
	}
	if s.HighlightColor == "" {
		s.HighlightColor = stockStatus.HighlightColor
	}
	// The remaining two (modify, completed) are not load-bearing
	// for any a10r role today — but keep them populated in case a
	// future role binds to them, and so debug dumps look complete.
	if s.ModifyColor == "" {
		s.ModifyColor = stockStatus.ModifyColor
	}
	if s.CompletedColor == "" {
		s.CompletedColor = stockStatus.CompletedColor
	}
}
