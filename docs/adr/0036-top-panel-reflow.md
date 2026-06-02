# 0036 — Top-panel reflow: drop logo, then shrink hint cols, then drop trailing hints

The **top panel** rendered fine on wide terminals but silently
clipped the **hint grid**'s right sub-columns when the natural
width exceeded `state.Width`. `RenderTop`'s tail call was
`format.PadRight(line, state.Width)` — pad-only, never truncate —
so an over-width line was sheared at the terminal's right edge.
The operator saw the leftmost hint sub-column (the keys their
muscle memory already knows) and lost the rest (the sort,
refresh, watch verbs they probably wanted to discover).
Reported as "the top header with the tenant shortcut and
keybinding is broken when the terminal is resized" with a
narrow-terminal screenshot showing precisely this clip.

This ADR records the priority order for width-driven reflow:

1. **Drop the logo** (existing behaviour, kept). The ASCII art is
   pure ornament — the first thing to go.
2. **Tenant shortcut grid takes its natural cols width**; the
   **hint grid** absorbs the remaining budget. Tenants are the
   multi-tenant contract — losing `<3>` for a configured backend
   silently would corrupt the operator's keybinding model. The
   hint grid has a fallback (`?` always opens the full **help
   overlay**).
3. **Step hint cols down 3 → 2 → 1** to fit `state.Width −
   tenantW − gap`. Same `<key> Description` cells, fewer
   columns, more rows up to the cap.
4. **Strict 5-row cap on the panel** (≡ logo height). When the
   logo drops, the panel does not grow vertically to fit every
   binding; trailing hints clip instead. The alternative — let
   the panel grow to 8–10 rows on narrow terminals — would
   invert the chrome principle (chrome stays slim so the body
   dominates) for an edge-case width, exactly when the operator
   already has the least vertical space.
5. **Drop trailing hints** once 1-col-at-`cellW` still won't fit,
   recomputing `cellW` after each drop so the widest survivor
   keeps shrinking the residual. Same drop-from-end semantics
   `header.renderHintsWithBudget` already uses, deliberately
   re-implemented here rather than shared: `header` lays out a
   single-line right-aligned strip, the panel lays out a
   column-major grid bounded by a logo-height row cap. The two
   diverge on what "drop" returns (text vs grid cell list) and
   on whether cellW matters (only the grid pads to cellW), so a
   shared helper would either leak the grid shape into the
   header strip or vice-versa. Reassessment is welcome when a
   third caller appears.

The contract this creates: **pages register hints most-important-
first**. Every page already does this implicitly (`Enter`,
`Space`, page verbs, then sort directions, then global `r` / `w`,
finally `?`); this ADR makes it a documented invariant so future
binding additions know where to slot in. The drop-from-end rule
is only honest if registration order is intentional.

Considered and rejected: (a) **let the panel grow vertically to
fit every binding** — body shrinks under the same narrow terminal
that triggered the reflow, a worse outcome than losing a binding
the operator can still find via `?`; (b) **truncate descriptions
with `…`** — keeps every chip visible but produces strings like
`<w> toggle…` that don't read as actionable; the chip alone is
more honest about "you know this binding or you don't"; (c)
**chip-only mode below a width threshold** — strip every
description on narrow terminals; teaches nothing about
unfamiliar bindings; (d) **shrink the tenant shortcut grid
first** — inverts tenants-first priority, leaves a power user
with six configured tenants seeing only three numeric shortcuts;
(e) **hard-clip at `state.Width` with a `…` indicator on the
clipped tail** — surfaces the truncation but the visible
content stays the same arbitrary slice the terminal would have
shown anyway, no honest improvement.
