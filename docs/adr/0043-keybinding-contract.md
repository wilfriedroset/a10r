# 0043 — Keybinding contract: vim + k9s parity, layered dispatch

a10r's keyboard surface follows two anchors at once: vim
motion (`j`/`k`/`h`/`l`, `gg`, `Shift+G`, `Ctrl+D`/`U`/`F`/`B`,
`/` to filter) and k9s parity (`:` command mode, `?` help,
`Space` to mark, the same-key-different-N bulk convention). The
two rarely conflict; where they do, vim wins on motion (e.g.
`PageDown` is half-page, not full, because `Ctrl+D` already
owns half-page and the vim reading is the one the operator
expects). This ADR records the durable contract. It does not
reproduce the live binding list — that catalog lives in code
under `internal/tui/` (the `action` layer plus each page's
handler registration) and in the user cheat-sheet
`docs/end-users/keybindings.md`. When the catalog and this ADR
diverge, the catalog is the live truth and this ADR is the
shape it must keep.

**Layered dispatch with fixed precedence.** A key is offered to
handlers in order, first consumer wins: modal (confirm,
picker, help) → prompt (`:`/`/`, which captures everything but
`Esc` while open) → per-view → table-context (only when the
body is a table) → global. `Esc` always reaches an open
modal/prompt to dismiss it, and otherwise falls through to
pop the page stack at the global layer. Modals are not stack
frames; `Esc` dismisses the modal without popping the view
under it.

**Global vs page binding model.** A binding is global when it
must behave identically on every page, page-local when its
verb is view-specific. The split is enforced, not stylistic:
numeric tenant quick-switch (`0` all, `1`–`9` nth configured
backend) and `?` help are registered once at the global layer
and emit a message every page reacts to — a per-page duplicate
would be dead code the dispatcher never reaches. Conversely `?`
must not appear in any page's right-hand hint strip; that
column is for view verbs, and help is advertised by the global
prelude chip (ADR 0038).

**Reserved-key policy.** A fixed set of load-bearing controls
is reserved and must not be rebound by future plugins (deferred
past v0.1, k9s pattern): `:` `/` `?` `Esc` `Ctrl+C` `Ctrl+T`
`Ctrl+E` `Ctrl+N` `Ctrl+\`, the digits `0`–`9`, the vim motions,
`Enter`, `Space`, `Ctrl+A`, `r`, `q`, `Tab`/`Shift+Tab`. Some
are reserved before they are bound (`Ctrl+N` is held for a
future compose-as-YAML companion to `Ctrl+E`) so the namespace
stays stable.

**Namespace discipline.** `Shift+<letter>` is sort-only and
never destructive or stateful — it always sorts by a column.
Bulk verbs reuse the single-row key and branch on the marked-
row count (`s` silences the cursor alert or fans out over
marks; `x` expires one silence or many), so there is no
parallel `Ctrl+S`/`Ctrl+X`; `Ctrl+\` is the explicit clear-all-
marks escape hatch.

**Dangerous-action tagging for read-only mode.** Every binding
that mutates remote state (silence, expire, edit) is tagged
Dangerous at registration. When the active backend or the
global override sets `read_only: true`, tagged bindings are
hidden from both the help overlay and the hint strip, and a
press is a no-op with a flash naming the read-only backend.
The tag is the single source for this filtering — read-only
mode is not a second list to maintain.

Related: ADR 0010 fixes the canonical key form bindings parse
into (`Shift+X`, `Ctrl+X`, chords); ADR 0037 governs how a
binding renders as a hint chip; ADR 0038 covers the
discoverability prelude and the help overlay's COMMANDS column.

Considered and rejected: (a) **embedding the full live binding
table in this ADR** — it is ~270 lines of per-view catalog that
churns with every page; duplicating it here guarantees drift
and two sources of truth, so the catalog stays in code and the
cheat-sheet and the ADR holds only the contract; (b) **per-page
numeric and help bindings** — symmetrical-looking but dead code,
since the global layer consumes those keys before any page sees
them; (c) **a separate read-only binding set** instead of a
Dangerous tag — a parallel list that must be kept in lockstep
with the real bindings, versus one flag read at filter time;
(d) **collapsing the layer stack** (one flat binding map with
priorities) — loses the clean "prompt captures everything but
Esc" and "table-context only when a table is mounted" rules
that the ordered layers give for free.
