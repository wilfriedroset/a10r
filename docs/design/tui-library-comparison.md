# TUI Library Comparison: bubbletea vs tview

## Decision

**bubbletea.**

a10r is a pet project without hard delivery constraints. Under that
framing, the factors that originally favoured tview ("fastest path to
k9s parity with built-in widgets") are worth less, and the factors
that favour bubbletea (cleaner Model/Update/View code, testability
via `teatest`, a thriving ecosystem, a visibly-on-the-upswing
maintainer) are worth more.

Concretely:

- What `bubbles` already ships covers ~80% of what a k9s-shaped TUI
  needs: table, list, textinput, viewport, help, paginator, spinner,
  key, cursor, textarea.
- The missing pieces (modal overlay, command palette, page/view
  stack, focus delegation, hotkey hints strip) are each small,
  declarative state machines — well-suited to the M/U/V shape.
- The charmbracelet ecosystem (lipgloss, huh, glamour, log, vhs,
  teatest, bubblezone, harmonica) composes cleanly on top of
  bubbletea; tview has no comparable constellation.
- tview is mature but near-stagnant (v0.42, small core team, no
  tests in the repo, no companion libs). It will keep working, but
  "stable" is not the same as "thriving."

The rest of this document is the trade-off record that led to this
decision — including the earlier recommendation, which was tview —
and an audit of the Charm ecosystem we intend to pull from.

Revisit the decision only if: we hit a concrete wall building a
widget that tview gives for free (most likely candidate: a dense
multi-column table with incremental updates at high frequency), or
if the project's scope shifts so hard that k9s-parity becomes a
delivery deadline rather than an aesthetic target.

## Charm ecosystem audit

What we plan to use, what's on the shelf, and what we're skipping.

### Almost certain to use

- **[bubbletea](https://github.com/charmbracelet/bubbletea)** (42k★)
  — The framework. Model/Update/View, message loop, `Program.Send`
  for injecting poll results from background goroutines.
- **[bubbles](https://github.com/charmbracelet/bubbles)** (8.3k★) —
  Ready-made widgets. We'll lean on `table`, `list`, `textinput`,
  `viewport`, `help`, `key`, `spinner`, `paginator`, `cursor`.
- **[lipgloss](https://github.com/charmbracelet/lipgloss)** (11k★) —
  Styling and layout primitives (`JoinVertical`, `JoinHorizontal`,
  `Place`, borders, padding, adaptive colours). This is also how
  we'll compose modal overlays and the header/body/footer frame.
- **[x/exp/teatest](https://github.com/charmbracelet/x)** — Golden-file
  snapshot testing for bubbletea programs. Drives a `Program`
  headlessly, feeds key events, asserts on rendered output. The
  answer to "how do you test a TUI without pixels."
- **[x/exp/golden](https://github.com/charmbracelet/x)** — Generic
  golden-file helper used by teatest; useful for model output
  snapshots even without a full Program.

### Likely to use once we get past the MVP

- **[huh](https://github.com/charmbracelet/huh)** (6.8k★) — Forms.
  Creating a silence is a form (matchers, duration, creator,
  comment); `huh` handles validation, focus, and layout and embeds
  into a bubbletea model via `huh.Form.Update`.
- **[glamour](https://github.com/charmbracelet/glamour)** (3.4k★) —
  Markdown renderer. Alertmanager annotations commonly hold runbook
  links and descriptions; rendering the `description`/`runbook_url`
  body inline in the alert-detail pane would be a nice touch.
- **[bubblezone](https://github.com/lrstanley/bubblezone)** (not
  official charm, but canonical) — Zero-width markers to detect
  mouse clicks on regions. Only needed if we commit to mouse
  support; fine to defer.

### On the shelf, probably not

- **[log](https://github.com/charmbracelet/log)** (3.2k★) — Pretty
  structured logger with a `slog.Handler` adapter. Considered as the
  default logger but dropped (open question D3): the configurable
  log format is JSON or logfmt only, and stdlib `slog.TextHandler`
  already emits logfmt-shaped output, so `log/slog` covers everything
  with zero extra deps. We'd only revisit if pretty TTY output for
  CLI subcommands becomes a goal.
- **[gum](https://github.com/charmbracelet/gum)** (23k★) — Shell-script
  UI helpers. Wrong layer for us (we're the app, not a script).
- **[vhs](https://github.com/charmbracelet/vhs)** (19k★) — Records
  terminal sessions to GIF/video. Useful for `README.md` demos when
  the tool is ready to show off, not for development.
- **[wish](https://github.com/charmbracelet/wish)** (5.1k★) — SSH-app
  server. Interesting future play (serve a10r as an SSH service
  against a shared Alertmanager), but not for v1.
- **[harmonica](https://github.com/charmbracelet/harmonica)** (1.5k★)
  — Spring physics for animation. Overkill for a status-tool TUI.
- **[colorprofile](https://github.com/charmbracelet/colorprofile)**,
  **[ultraviolet](https://github.com/charmbracelet/ultraviolet)**,
  **[ansi](https://github.com/charmbracelet/x)**,
  **[cellbuf](https://github.com/charmbracelet/x)** — Lower-level
  bricks that bubbletea/lipgloss consume internally. Don't reach
  past the high-level libs unless we have to.

### Not applicable

- **[catwalk](https://github.com/charmbracelet/catwalk)** (LLM
  providers), **[soft-serve](https://github.com/charmbracelet/soft-serve)**
  (git server), **[sequin](https://github.com/charmbracelet/sequin)**
  (ANSI debugging CLI), **[x/mosaic](https://github.com/charmbracelet/x)**
  (image-to-terminal), **[x/sshkey](https://github.com/charmbracelet/x)**
  — Nothing to do with this project.

Health of the org in one line: bubbletea v2 just shipped (Go 1.25),
the flagship libs each have thousands to tens of thousands of stars,
the `x/exp/*` staging ground keeps producing useful infra (teatest,
golden, vcr), and the company behind it ships regularly. Low
ecosystem risk.

---

## Context

`a10r` aims to be a modern, fast, intuitive TUI for Alertmanager with a
look-and-feel close to [k9s](https://github.com/derailed/k9s): a header
with context info, a main body driven by a dense, filterable, sortable
table, a command-prompt bar (`:alerts`, `:silences`, `/filter`…),
contextual hotkeys shown at the bottom, modal dialogs for confirmations
(ack / silence / delete), and real-time updates pushed from background
goroutines polling the Alertmanager API.

Two Go libraries are the obvious candidates:

- [bubbletea](https://github.com/charmbracelet/bubbletea) — Elm-style,
  functional, part of the Charm ecosystem.
- [tview](https://github.com/rivo/tview) — widget-based, imperative,
  built on top of `tcell`.

Requirements, in priority order:

1. Simple to use and extend.
2. Ready-to-use building blocks (table, flex/grid layout, modal,
   command prompt, pages).
3. Easy to maintain and test.
4. Vibrant community.
5. Good documentation.

## What k9s actually uses

Knowing what k9s uses is the single strongest signal for a k9s-like
TUI. k9s is built on **tview** (a
[derailed/tview](https://github.com/derailed/tview) fork) on top of a
[derailed/tcell](https://github.com/derailed/tcell) fork — see its
`go.mod`.

The shape of k9s is revealing:

- `internal/ui` wraps tview primitives directly: `Table` embeds
  `*tview.Table` (`internal/ui/table.go`), `Prompt` wraps
  `*tview.TextView` (`internal/ui/prompt.go`), `Menu` wraps
  `*tview.Table` for dynamic hints, `Pages` wraps `*tview.Pages`,
  `App` embeds `*tview.Application`.
- Layout is a `*tview.Flex` with four rows: status indicator, content,
  crumbs, flash (`internal/view/app.go`).
- Modal dialogs (confirm, delete, prompt) are built on `*tview.Modal`,
  `*tview.Form` and `*tview.Frame`.
- Async updates use tview's `QueueUpdateDraw` (`internal/ui/app.go`)
  wrapped in a goroutine: watchers call
  `app.QueueUpdateDraw(func(){ view.UpdateUI(data) })`.
- Theming is YAML-driven, with a listener pattern notifying every
  widget on style changes (`internal/config/styles.go`).
- Keybindings are a `KeyActions` registry mapping `tcell.Key` to
  `KeyAction` (`internal/ui/action.go`); actions are layered
  (app-level, view-level) and `Hints()` drive the footer menu.

None of this is accidental — it matches exactly the widgets tview
ships. Reproducing this architecture on top of bubbletea is possible,
but it means re-implementing a large part of the stack.

## Side-by-side

| Dimension | bubbletea | tview |
|---|---|---|
| Programming model | Elm: `Init/Update/View`, immutable `Model`, `Cmd`/`Msg` loop | Imperative: `Application` + mutable `Primitive` widgets, event callbacks |
| Core interface | `Model` (`tea.go` L52–L65) | `Primitive` (`primitive.go` L6) |
| Layout | No built-in layout; compose with lipgloss strings and focus-switching | `Flex` (`flex.go` L33), `Grid` (`grid.go` L31), `Pages` (`pages.go` L20) |
| Table | Provided by `bubbles/table` (external lib), sortable/filterable | `tview.Table` (`table.go` L464) with `SetSelectable`, `SetFixed`, per-cell styling — no built-in sort |
| Modal dialog | Build yourself: overlay view + focus | `tview.Modal` (`modal.go` L12) out of the box |
| Command prompt / autocomplete | Compose `textinput` + filtered `list` (see examples/autocomplete) | `InputField` (`inputfield.go` L78) with `SetAutocompleteFunc`, Pages overlay |
| Focus handling | Manual: model decides who owns input | Automatic: `Application.SetFocus`, `Primitive.Focus/Blur/HasFocus` |
| Styling | [lipgloss](https://github.com/charmbracelet/lipgloss): chainable styles, borders, gradients | `tcell.Style` per cell + `tview.Styles` global theme + color tags (`[yellow]text[white]`) |
| Async updates | `Program.Send(msg)` (`tea.go` L1183–L1188) into the update loop | `Application.QueueUpdate(Draw)` (`application.go` L915, L924) |
| Timers / polling | `tea.Tick`, `tea.Every`, `Batch`, `Sequence` (`commands.go`) | Plain goroutines + `QueueUpdateDraw` |
| Testing | `teatest` from `charmbracelet/x/exp/teatest` (external, golden-file snapshots); `Model` is trivially unit-testable — no terminal needed | No first-party test helpers; 0 `_test.go` files in the repo. k9s itself mocks the data layer (`Tabular` interface) and does not test rendering |
| Ecosystem | `bubbles` (ready-made widgets), `lipgloss`, `huh`, `glamour`, `wish`, `bubblezone`, `harmonica` | Just tview + tcell |
| Examples | 66 runnable examples in-repo, plus per-component examples in `bubbles` | 21 demo programs in `/demos`, plus a self-hosted presentation demo |
| Docs | Rich README with tutorial, `tutorials/basics`, `tutorials/commands` | Thorough `doc.go` (224 lines), README, external wiki |
| Versioning | v2 (v2.0.6) — clean, modern, `go 1.25` | v0.42.0, stable for years, single major line |
| Community | ~164 contributors, 18k+ dependents; used by Azure CLI, AWS, Cockroach, MinIO, Ubuntu, most Charm projects | Smaller core (Oliver, rivo, Chris Miller dominate); ~830 commits, steady |
| Mobile/Web/IDE reach | Works anywhere a terminal works; mouse + enhanced keyboard in v2 | tcell handles terminal abstraction; mouse/paste supported |

## For a k9s-like Alertmanager TUI

### The case for tview

- **Everything k9s uses is a built-in widget.** Table, Flex, Pages,
  Modal, Form, InputField, Frame, TextView — you assemble, you don't
  build. For the screens we need (alerts list, silences list,
  receivers, alert detail, silence form, confirm dialogs) this is the
  shortest path to a working prototype.
- **Focus management is solved.** `Application.SetFocus` plus
  `Primitive.InputHandler` gives you app-level vs widget-level key
  capture without plumbing focus state through every model.
- **Async story matches Alertmanager polling.** A background
  goroutine polling `/api/v2/alerts` that ends with
  `app.QueueUpdateDraw(func(){ table.SetCell(...) })` is idiomatic
  and race-free.
- **Prior art.** k9s is tview; so is
  [podman-tui](https://github.com/containers/podman-tui),
  [lazydocker](https://github.com/jesseduffield/lazydocker) (gocui, a
  cousin), [cointop](https://github.com/cointop-sh/cointop). The
  pattern library is rich.
- **Imperative mutation maps to "refresh table from API" cleanly.**
  You don't rebuild a full view tree on each tick; you mutate the
  cells that changed.

### The cost of tview

- **Smaller ecosystem.** No `bubbles`-equivalent, no `lipgloss`
  equivalent. You get what ships plus what you build.
- **No first-party test helpers, no tests in the repo.** The library
  itself is not tested, and neither is k9s's UI layer (they test the
  model/data layer behind interfaces). You will do the same: test
  data transformations and the `Tabular`-style adapters, not
  rendering.
- **Styling is less expressive.** Color tags in strings + `tcell.Style`
  per cell works, but feels dated next to lipgloss. Fine for a k9s
  clone; not glamorous.
- **No built-in sorting on Table.** You sort your data slice and
  re-populate cells. Not hard, but explicit.
- **Single-maintainer cadence.** tview is maintained but not
  hyperactive; the charm ecosystem ships faster.

### The case for bubbletea

- **Clean mental model.** `Model`/`Update`/`View` is easy to reason
  about, easy to onboard contributors to, trivial to unit-test:
  `m2, cmd := m.Update(KeyMsg{...}); assert.Equal(t, expected, m2)`.
  No terminal required.
- **`teatest` exists** and does golden-file snapshot tests of full
  programs — something tview fundamentally cannot match.
- **Ecosystem momentum.** bubbles (list, table, textinput, viewport,
  help, paginator, spinner, progress), lipgloss (rich styling), huh
  (forms), glamour (markdown). If we ever want Markdown-rendered
  runbooks inline, it's a one-liner.
- **Real-time updates are idiomatic.** `Program.Send(alertsMsg{...})`
  from a poll goroutine, and `Update` dispatches. No mutex
  discipline needed.
- **Modern, active, mainstream.** v2 is recent, Go 1.25, 18k+
  dependents, used across the industry.

### The cost of bubbletea

- **No native modal, no native command palette, no native focus
  manager.** You build them — not hard, but it's code you own. For a
  k9s clone, that means re-implementing what tview hands you.
- **Layouts are strings.** Composition uses `lipgloss.JoinVertical` /
  `JoinHorizontal` plus careful sizing. For a dense, multi-pane UI
  with resizable splits this gets fiddly faster than tview's `Flex`.
- **Table widget is external and less mature** than tview's. Sorting,
  fixed columns, per-cell coloring, large-dataset scrolling are
  workable but not as battle-tested as tview's `Table`, which has
  driven k9s through many Kubernetes release cycles.
- **Re-render-the-world model** is philosophically clean but imposes
  a cost for continuously-updating dense tables. In practice fine
  for Alertmanager volumes; worth noting.
- **No obvious path to k9s parity without rebuilding a mini-tview.**
  Every k9s look-and-feel detail — the menu hints strip, the command
  prompt with `:` vs `/` modes, the layered modal stack, the
  breadcrumbs — is a tview primitive we'd rewrite.

## Earlier recommendation (superseded): tview

> **Note:** The analysis below was written under the assumption that
> "look-and-feel similar to k9s" implied a hard delivery constraint.
> It is preserved as the record of the trade. The final decision is
> bubbletea (see the Decision section at the top).

The case for tview, in order of importance at the time:

1. **Ready-to-use building blocks (requirement #2) is where tview
   dominates.** Table, Flex, Pages, Modal, InputField, Form, Frame
   are exactly the vocabulary of a k9s-like TUI. bubbletea requires
   building most of these from lower primitives. For a single
   maintainer who wants to ship a useful tool fast, that gap is
   decisive.
2. **"Simple to use and extend" (requirement #1)** reads differently
   for each lib: bubbletea's *model* is simpler, but tview's
   *delivery* of a k9s-shape UI is simpler. Since the goal is
   explicitly "look-and-feel similar to k9s," we should optimize for
   the second reading.
3. **Maintainability and testing (requirement #3).** bubbletea has
   the cleaner testability story in theory. In practice, k9s's
   approach — test the data/model layer behind interfaces, skip
   rendering tests — has proven sufficient for a large production
   tool, and we can adopt it directly. That neutralizes most of
   bubbletea's testing advantage.
4. **Community and docs (requirements #4, #5).** bubbletea wins on
   raw community size and doc polish, but tview's docs and demos are
   sufficient, and the *relevant* community for us is the k9s-shaped
   TUI community, which lives on tview.

## Proposed architectural sketch (bubbletea)

Same intent as the k9s layout, shaped for Model/Update/View:

```
internal/
  tui/
    app.go              # root tea.Model: holds current page, dispatches keys/msgs
    pages.go            # page stack + transitions (push/pop, no re-render of world)
    keys.go             # key.Binding map, shared + per-page
    styles.go           # lipgloss styles loaded from YAML skin
    components/
      header.go         # status strip: endpoint, filter, counts
      footer.go         # dynamic hotkey hints (bubbles/help driven)
      prompt.go         # : (command) and / (filter) modes over bubbles/textinput
      modal.go          # overlay via lipgloss.Place + focus-aware Update
      table.go          # thin wrapper over bubbles/table with our sort/filter
    pages/
      alerts.go         # alerts list page
      silences.go       # silences list page
      receivers.go      # receivers page
      alert_detail.go   # viewport + glamour-rendered annotations
      silence_form.go   # huh.Form embedded as a page
  model/
    alerts.go           # poller goroutine, emits tea.Msg on each refresh
    silences.go
    client/             # Alertmanager API client (pure, testable, no TUI)
  config/
    styles.yaml         # default skin
    keys.yaml           # keybindings
```

Message / command flow:

- Polling goroutines run outside the `Program` and call
  `program.Send(alertsRefreshedMsg{...})` on each tick.
- User input -> root `Update` -> active page `Update`. Modals and the
  command prompt are focus-capturing children; the root routes all
  keys to them while visible.
- No manual draw scheduling: bubbletea re-renders when any `Update`
  returns a new Model. Large tables use bubbles/table's internal
  diffing; we keep our data slices sorted in-place to avoid
  allocating every tick.

Testing strategy:

- `model/client/*` and `config/*`: classic unit tests against
  recorded Alertmanager fixtures.
- `tui/components/*` and `tui/pages/*`: unit-test the `Update`
  function directly — feed messages, assert on the returned Model.
  No terminal needed.
- `tui/app.go` end-to-end: `teatest.NewTestModel` driving the full
  `Program`, asserting on golden snapshots of `View()` output for
  key flows (open alerts -> filter -> drill into detail ->
  create-silence -> confirm).
- Keep golden files small and scoped; use `x/exp/golden` to update
  them with `-update`.

## Appendix A: k9s is not a hard target

The UI vocabulary we're borrowing from k9s is: header with context,
dense filterable/sortable table as the main body, `:` command mode,
`/` filter mode, hotkey hints strip, modal dialogs for confirm /
form flows, and breadcrumb-style page stack. None of those require
tview's specific primitives — they require *their bubbletea
equivalents*, which we write once and reuse across pages.

Where we'll deviate from k9s by default, because bubbletea makes it
cheap:

- Markdown-rendered alert annotations (glamour) in the detail pane.
- huh-driven silence form with inline validation instead of a
  scrolling Form widget.
- A two-layer YAML skin schema (palette + roles) compiled to
  lipgloss styles, with multiple bundled themes embedded in the
  binary. (Adaptive light/dark and live reload were considered but
  deferred — see open-question M1 / `docs/design/theming.md`.)

## Appendix B: key references

k9s (tview-based reference implementation):
- `internal/ui/app.go` — App wrapper and `QueueUpdateDraw`.
- `internal/ui/table.go:38` — Table primitive.
- `internal/view/app.go:44` — Application controller.
- `internal/view/app.go:166` — Layout composition.
- `internal/ui/action.go:32` — Keybinding registry.
- `internal/config/styles.go:52` — Theme system.

tview:
- `application.go:72` — `Application` type.
- `application.go:915` / `:924` — `QueueUpdate` / `QueueUpdateDraw`.
- `primitive.go:6` — `Primitive` interface.
- `table.go:464` — `Table` widget.
- `pages.go:20`, `flex.go:33`, `grid.go:31`, `modal.go:12`,
  `inputfield.go:78`, `form.go:63` — widget definitions.
- `styles.go:23` — global `Styles` theme.

bubbletea:
- `tea.go:52` — `Model` interface.
- `tea.go:426` — `Program` type.
- `tea.go:1183` — `Program.Send` (external event injection).
- `commands.go` — `Batch`, `Sequence`, `Tick`, `Every`.
- Companion: `bubbles/table`, `bubbles/list`, `bubbles/textinput`,
  `bubbles/viewport`, `bubbles/help`, `bubbles/key`.
- Styling: `lipgloss`.
