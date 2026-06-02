// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// fieldIndex enumerates the form's input slots so Tab navigation
// can walk them in display order. fieldTenant sits at position 0
// per ADR-0011 — the form owns its tenant selection and renders
// it as the first row, above Matchers. The row is disabled (skipped
// by cycleFocus, no leading marker) when the form has only one
// client, when it's in edit mode (a silence cannot move between
// tenants in the AM v2 API), or when bulk mode is active (the
// Targets banner replaces it entirely).
type fieldIndex int

const (
	fieldTenant fieldIndex = iota
	fieldMatchers
	fieldStarts
	fieldEnds
	fieldCreator
	fieldComment
	numFields
)

// matchersHeight is the number of visible rows reserved for the
// matchers textarea. Six is enough for the typical 2-3-line
// silence without forcing a scroll.
const matchersHeight = 6

// newInput constructs a textinput.Model with the form's shared
// shape: no built-in prompt (the row label provides one), the
// supplied placeholder for empty state, and no character limit.
//
// Typed text is forced to the body's default foreground in both
// focused and blurred states so a filled row never reads as
// stale — bubbles' default paints blurred text in dim grey,
// which collides with the form's focus marker (a leading `▸`
// plus the accent-tinted label) and made every blurred-but-
// filled row look disabled.
//
// Placeholder dim is deliberately kept on both states per
// ADR-0012 so the operator can distinguish an empty field
// ("$USER", "2h", …) from one carrying a real value at a
// glance. The placeholder colour is foreground-only — no
// background paint — so the chrome-on-default-bg rule is
// preserved.
func newInput(placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	s := in.Styles()
	s.Blurred.Text = s.Focused.Text
	in.SetStyles(s)
	return in
}

// flattenTextareaBlur strips the bubbles defaults that would
// fight the form's focus chrome. Two slots are flattened:
//   - Text in both focused and blurred states, so typed
//     matchers stay at default fg whichever row owns focus
//     (bubbles' blurred default dims text and made filled
//     rows read as stale);
//   - CursorLine in both states, so the active line never
//     paints a background stripe behind the buffer (the
//     chrome-on-default-bg rule).
//
// Placeholder is intentionally left at the bubbles default
// per ADR-0012 — the dim foreground is what distinguishes an
// empty matchers buffer from a populated one. The default is
// foreground-only ("alertname=HighCPU\nseverity=critical" in
// grey), no background paint, so the no-stripe rule still
// holds.
func flattenTextareaBlur(m *textarea.Model) {
	s := m.Styles()
	bare := lipgloss.NewStyle()
	s.Focused.Text = bare
	s.Blurred.Text = bare
	s.Focused.CursorLine = bare
	s.Blurred.CursorLine = bare
	m.SetStyles(s)
}

// forwardToFocused dispatches the message to whichever bubbles
// input is currently focused. Each model's Update returns a
// fresh copy (value receiver), so the slot is reassigned in
// place. Accepts tea.Msg (not just KeyPressMsg) so cursor blink
// ticks and paste completions reach the focused field too.
func (f *Form) forwardToFocused(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch f.focus {
	case fieldTenant:
		// Tenant has no bubbles input — it's a read-out for the
		// active selection, modified only via the picker. Drop the
		// message rather than routing it anywhere; the cursor blink
		// loop is keyed off the other fields' Focus() Cmds.
	case fieldMatchers:
		f.matchers, cmd = f.matchers.Update(msg)
	case fieldStarts:
		f.starts, cmd = f.starts.Update(msg)
	case fieldEnds:
		f.ends, cmd = f.ends.Update(msg)
	case fieldCreator:
		f.creator, cmd = f.creator.Update(msg)
	case fieldComment:
		f.comment, cmd = f.comment.Update(msg)
	case numFields:
		// Sentinel — never the active focus.
	}
	return cmd
}

// cycleFocus walks focus by delta (typically ±1), blurring the
// outgoing field and focusing the incoming one. Returns the
// Cmd Focus emits (cursor blink schedule) so the program loop
// drives the new field's blink timer.
//
// Two kinds of fields are skipped on the way:
//   - fieldMatchers in bulk mode (the textarea is hidden, the
//     Targets banner is non-focusable);
//   - fieldTenant when tenantDisabled() — single-client / edit-mode /
//     bulk; the row either isn't rendered or renders read-only.
//
// The skip loop runs at most numFields-1 times to guarantee
// termination even in a future shape where every slot is disabled
// (defensive — not reachable today).
func (f *Form) cycleFocus(delta int) tea.Cmd {
	f.activeBlur()
	for range int(numFields) {
		f.focus = (f.focus + fieldIndex(delta) + numFields) % numFields
		if !f.focusDisabled() {
			break
		}
	}
	return f.activeFocus()
}

// focusDisabled reports whether the slot at f.focus is one the
// cycle must skip. Mirrors the renderer's omission rules so Tab
// never lands on a row the user can't actually edit. The other
// fields (Starts/Ends/Creator/Comment/numFields) are always
// focusable / sentinel, so they take the default-false branch.
func (f *Form) focusDisabled() bool {
	switch f.focus {
	case fieldTenant:
		return f.tenantDisabled()
	case fieldMatchers:
		return f.bulk
	case fieldStarts, fieldEnds, fieldCreator, fieldComment, numFields:
		return false
	}
	return false
}

// activeFocus calls Focus on the field at the current index.
// fieldTenant has no bubbles input — the row is a static display
// of the active selection, so the focus call is a no-op there.
func (f *Form) activeFocus() tea.Cmd {
	switch f.focus {
	case fieldTenant:
		return nil
	case fieldMatchers:
		return f.matchers.Focus()
	case fieldStarts:
		return f.starts.Focus()
	case fieldEnds:
		return f.ends.Focus()
	case fieldCreator:
		return f.creator.Focus()
	case fieldComment:
		return f.comment.Focus()
	case numFields:
	}
	return nil
}

// activeBlur calls Blur on the field at the current index.
func (f *Form) activeBlur() {
	switch f.focus {
	case fieldTenant:
		// No bubbles input behind this row — nothing to blur.
	case fieldMatchers:
		f.matchers.Blur()
	case fieldStarts:
		f.starts.Blur()
	case fieldEnds:
		f.ends.Blur()
	case fieldCreator:
		f.creator.Blur()
	case fieldComment:
		f.comment.Blur()
	case numFields:
	}
}
