# 0012 — Form placeholders render dim in both focused and blurred states

`internal/tui/form/silence/form.go`'s `newInput` previously flattened
both `Styles.Focused.Placeholder` and `Styles.Blurred.Placeholder` to
the same colour as typed text, so an empty field with a placeholder
("now", "2h", "$USER", "ack while patching") was visually
indistinguishable from a pre-filled value. The original rationale —
documented inline at the helper — was that bubbles' defaults paint
dim blurred text **and** dim placeholders together, producing three
competing dim signals on every blurred row (text, placeholder,
chrome) that read as a stale form. This ADR un-flattens placeholder
styling in both states: blurred text was already overridden to
default foreground (`s.Blurred.Text = s.Focused.Text` stays), so the
only remaining dim signal on a blurred row is the placeholder itself,
which does not read as stale because the row's typed text and label
remain at default brightness. The leading `▸` marker plus accent
label still owns focus signalling; placeholder dim purely serves the
"empty vs. filled" axis.

Italic was considered and rejected because Konsole's stock skin and
tmux without italic passthrough render italic as reverse-video or
not at all — a cue that disappears on the wrong terminal is worse
than no cue. The "stale row" regression the original flatten was
solving is explicitly preserved against in tests: a blurred row with
a typed value must read at default fg, not dim.
