# 0037 — Hint chip case is k9s lowercase, with bare-uppercase shift-expansion

The **hint grid** and **help overlay** both render binding chips
via `help.ChipText`. Originally `ChipText(key)` returned
`"<" + key + ">"` verbatim — `<Shift+S>`, `<Enter>`, `<Ctrl+E>`,
`<S>`. K9s renders the same surface as `<shift-s>`, `<enter>`,
`<ctrl-e>`: lower-case throughout, `-` as the modifier separator.
The user wanted a10r aligned with k9s here for the same reason
the panel is k9s-shaped at all: muscle memory and visual parity
for operators who already know k9s.

The chosen rule (one line in `ChipText`):

1. **Lowercase the whole key**, then **swap `+` to `-`**. So
   `Shift+S` → `<shift-s>`, `Ctrl+E` → `<ctrl-e>`, `Enter` →
   `<enter>`, `/` → `</>`. Ligature-prone keys (`-`, `=`, `<`,
   `>`) keep their `[…]` square-bracket form — that rule is
   orthogonal and pre-dates this ADR.
2. **A bare uppercase single letter expands first**: `S` rewrites
   to `Shift+S` before step 1, so it renders as `<shift-s>`. This
   is non-cosmetic — without it, alert detail binds both `s`
   (silence) and `S` (open silences) and they would both render
   `<s>` in the same hint grid, distinguished only by the
   description text. The expansion restores the visual
   disambiguation k9s gets for free from tcell (which delivers
   `Shift-S` to its `ToMnemonic`, never bare `S`).

The action layer is untouched. `action.Action.Key` keeps its
existing values (`"s"`, `"S"`, `"Shift+F"`, `"Ctrl+E"`,
`"Enter"`) and ADR 0010's canonical-form rules at user-keybinding
load time are unchanged. This ADR is purely about chip display.

Considered and rejected: (a) **lowercase only, keep `+`**
(`<shift+s>`, `<ctrl+e>`) — closer to a10r's internal canonical
key shape but loses the k9s visual match the rest of the panel
chrome chases; (b) **chord-only lowercase, word-keys stay
title-case** (`<shift-s>` but `<Enter>`) — splits the rule into
"modifiers like k9s, words not", a worse mental model than
either pure side; (c) **leave bare uppercase as `<s>` and accept
the collision with lowercase `<s>` on alert detail** — the
description column disambiguates in prose but the operator
scanning the grid for the second `<s>` reads it as a typo
before finding the chord.
