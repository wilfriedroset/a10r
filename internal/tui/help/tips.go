// SPDX-License-Identifier: Apache-2.0

package help

// Tip is one curated hint shown by the optional rotating hint bar
// (P2.W1.7 / scout doc §E8). Key is the keystroke or sigil being
// advertised; Description is the one-line "what it does" the user
// reads. Two fields rather than a single pre-formatted string so a
// future renderer can chip the key the same way the help overlay
// does (see help.entry / chipText) without re-parsing the line.
type Tip struct {
	Key  string
	Text string
}

// tips is the v0.0.1 curated set. Kept short on purpose — the hint
// bar rotates one at a time and a longer list dilutes the value of
// each entry. Items reference only features that have already
// landed on `feat/v0.0.1-phase-2-batch`; speculative knobs (custom
// keybindings, action registry surface, …) stay out until the
// matching plan items land. Order is the rotation order callers
// see when iterating Tips() index-by-index.
var tips = []Tip{
	{Key: "?", Text: "open the help overlay"},
	{Key: ":", Text: "command bar — try :alerts, :silences, :groups"},
	{Key: "/", Text: "filter the current list"},
	{Key: "~", Text: "prefix a filter with ~ for fuzzy matching"},
	{Key: "\\", Text: "prefix a filter with \\ to match literally"},
	{Key: "g / G", Text: "jump to top / bottom of the table"},
	{Key: "y", Text: "toggle raw YAML on alert and silence detail"},
	{Key: "w", Text: "toggle watch-mode on the alerts page"},
	{Key: "t", Text: "toggle relative / absolute timestamps"},
	{Key: "Ctrl+T", Text: "open the tenant picker"},
}

// Tips returns the curated tip catalogue as a fresh slice so callers
// can iterate without worrying about an aliased mutation. The
// rotating hint bar consumes this; the help overlay stays driven by
// the action registry as before.
func Tips() []Tip {
	out := make([]Tip, len(tips))
	copy(out, tips)
	return out
}
