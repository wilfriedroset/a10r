---
title: k9s look-and-feel audit
status: draft
audience: a10r designers and contributors
source: https://github.com/derailed/k9s (local clone at /home/debian/workspace/github.com/derailed/k9s)
---

# k9s look-and-feel audit

This document captures the UX patterns and implementation details of [k9s](https://github.com/derailed/k9s) that we want to echo in `a10r` (an Alertmanager TUI). We are **not** cloning k9s: Kubernetes resources and Alertmanager alerts are different domains. We are extracting the *feel* — keyboard-first, command-bar driven, table-centric, stack-navigated — so a user who knows k9s can be productive in `a10r` within minutes.

File paths below refer to the k9s tree at `/home/debian/workspace/github.com/derailed/k9s` unless stated otherwise.

---

## 1. Libraries and stack

### TUI layer

- `github.com/derailed/tview v0.8.5` — k9s uses a **fork** of [rivo/tview](https://github.com/rivo/tview). The fork carries tweaks (column sort arrows, key handling) that have not been upstreamed. See `internal/ui/app.go:16`, `internal/ui/table.go:39`, `internal/ui/pages.go:17`.
- `github.com/derailed/tcell/v2 v2.3.1-rc.4` — likewise a fork of `gdamore/tcell` pinned to match the tview fork. See `internal/ui/key.go:6`, `internal/ui/action.go:13`.

**Implication for a10r:** this document describes how *k9s* is built; a10r builds on bubbletea + bubbles + lipgloss instead (see `docs/design/tui-library-comparison.md` for the decision and trade-off record). Where this audit recommends a tview primitive, the recommendation translates to the equivalent bubbletea construction — those translations are spelled out in §11 below.

### CLI, config, logging

- `github.com/spf13/cobra v1.10.2` — CLI entry (`cmd/root.go:41`).
- `gopkg.in/yaml.v3 v3.0.1` — all config and skins are YAML.
- `github.com/xeipuuv/gojsonschema v1.2.0` — JSON-Schema validation for config files; schemas live in `internal/config/data`.
- `log/slog` (stdlib) + `github.com/lmittmann/tint v1.1.3` — structured logging to a rotating file under `$XDG_STATE_HOME/k9s`. Wire-up: `cmd/root.go:103-106`. k9s wraps slog with a small helper package `internal/slogs` for consistent field keys.

### Search and input

- `github.com/sahilm/fuzzy v0.1.1` — fuzzy matching (`internal/view/xray.go:26`).
- Custom fish-shell-style suggestion buffer at `internal/model/fish_buff.go:19-138` — no external lib, it is a ring buffer + arrow-key cycler. Small and worth copying the idea.

### Domain libs (out of scope for a10r, listed for completeness)

- `k8s.io/client-go`, `k8s.io/metrics`, `helm.sh/helm/v3`, `github.com/anchore/grype`, `github.com/anchore/syft`, `github.com/atotto/clipboard`.

---

## 2. Screen layout

k9s renders a single vertical flex (`internal/view/app.go:166-183`). From top to bottom:

```
┌─ Status indicator (1 line) ─────────────────────────────┐  connection, cluster, ns
├─ Header (only when !headless) ──────────────────────────┤
│  Logo (left, 6 lines)   │   Menu hints (right columns)  │
├─ Content (flex-grow) ───────────────────────────────────┤
│  Active view — table / tree / logs / yaml / form …      │
│                                                          │
├─ Crumbs (1 line) ───────────────────────────────────────┤  < pods > < default > < my-pod >
├─ Prompt (3 lines, shown on demand) ─────────────────────┤  `:cmd` or `/filter` + suggestions
├─ Flash (1 line) ────────────────────────────────────────┤  emoji + message, transient
└─────────────────────────────────────────────────────────┘
```

### Key components

| Component | File | Notes |
|---|---|---|
| Status indicator | `internal/ui/indicator.go:28-81` | Refreshes cluster/namespace every 15 s (`clusterRefresh = 15 * time.Second`). |
| Logo | `internal/ui/logo.go` | Two variants: `LogoSmall` (header) and `LogoBig` (splash). Colors vary by health. |
| Menu hints | `internal/ui/menu.go:26-216` | Dynamic from `KeyActions.Hints()`. Number keys (0-9) rendered in a dedicated left column; `maxRows = 6` per column then wraps. |
| Crumbs | `internal/ui/crumbs.go:15-74` | Stack-driven breadcrumbs. Active crumb highlighted in orange. |
| Prompt | `internal/ui/prompt.go:79-221` | Shows `:` (command) or `/` (filter). Suggestions rendered inline under the input. |
| Flash | `internal/ui/flash.go:22-114` | Emoji + colored text. Auto-clears after `DefaultFlashDelay`. |
| Splash | `internal/ui/splash.go:35-71` | Big logo + version, shown `splashDelay = 1 * time.Second` on boot. |

### Layout primitive

`tview.Flex` with explicit heights for status/crumbs/flash and `flex:0` for a growing content area. The Header is swappable (toggle headless mode with a keybind). Pages live inside the content slot so that modals and forms can replace the active view without rebuilding the chrome.

---

## 3. Navigation and interaction model

### Command bar (`:` prefix)

- Bound via `KeyColon = 58` (`internal/ui/app.go:41`).
- Accepts `:<alias> [args]`, e.g. `:pods`, `:ctx prod`, `:ns kube-system`.
- Suggestions from `FishBuff`:
  - Up/Down arrows cycle matches.
  - Tab or Ctrl-E accepts first suggestion.
  - ESC cancels.
- Aliases are resolved through `internal/view/command.go:8-180` against the alias registry.

### Filter (`/` prefix)

- Per-view filter buffer (`internal/ui/table.go:73`).
- Regex or text depending on the view's renderer.
- Ctrl-U clears, ESC cancels.
- Filter applies to visible rows in real-time.

### Key bindings

- Action registry: `internal/ui/action.go:16-221`. Each `KeyAction` carries metadata: `Visible`, `Shared`, `Dangerous`, `Plugin`, `HotKey`. "Dangerous" actions are filtered out in read-only mode (`internal/ui/table.go:94-100`).
- Numeric keys (`internal/ui/key.go:23-49`): `Key0`..`Key9` and `KeyShift0`..`KeyShift9` are first-class, used for container selection and quick context switching.
- Navigation: arrow keys + PgUp/PgDn for scroll; **Enter** drills down; **ESC** pops the stack; **Left/Right** select sort column.
- Selection: **Space** marks a row (multi-select), **Ctrl-A** toggles all.
- k9s does **not** implement full vim motions (no `gg`/`G`/`j`/`k`) — just arrows. Worth reconsidering for a10r.

### Read-only mode

Set via `k9s.yaml: k9s.readOnly: true` (`internal/config/k9s.go:43`). Dangerous actions (delete, scale, edit) vanish from the menu and are ignored on keypress.

### Modals and forms

- Confirmation dialogs: `tview.Modal` wrappers in `internal/view/confirm.go`.
- Forms (edit, port-forward, etc.): `tview.Form` pushed onto the page stack.

---

## 4. View architecture

### Abstractions

All views implement `ResourceViewer` (`internal/view/types.go`):

```go
type ResourceViewer interface {
    model.Component
    Init(context.Context) error
    Name() string
    GVR() *client.GVR
    // ... more
}
```

Views are **registered by GVR** in `internal/view/registrar.go:10-148` via `loadCustomViewers()`:

- `client.PodGVR` → `NewPod`
- `client.EvGVR` → `NewEvent`
- `client.CoGVR` → `NewContainer`
- …

Unregistered resources fall through to a generic table driven by the renderer package.

### View families

| Family | File | Purpose |
|---|---|---|
| Table / Browser | `internal/view/browser.go:34-148`, `internal/view/table.go` | Generic resource tables. 90% of k9s. |
| Tree (Xray) | `internal/view/xray.go:39-150` | Hierarchical relationships + fuzzy search. |
| Logs | `internal/view/log.go` | Multi-container log tail, follow mode, filter. |
| YAML / Details | `internal/view/yaml.go`, `details.go` | Read-only or edit mode. |
| Pulse | `internal/view/pulse.go:82-150` | Metrics sparkline dashboard. |
| Forms | `internal/view/*_extender.go` | Edit, port-forward, scale dialogs. |

### Page stack

`internal/ui/pages.go:15-110` wraps `tview.Pages` with a `model.Stack`:

- `Push(view)` / `Pop()` drive navigation.
- Stack events (`StackPushed`, `StackPopped`) are broadcast to listeners — **menu and crumbs both subscribe**, which is why they update automatically.
- ESC triggers `Pop()` unless the active view captured the key (e.g. prompt is open).

This is the single most copy-worthy architectural decision in k9s.

### Command layer

`internal/view/command.go:8-180`:

- Holds the alias registry and hotkey map.
- Parses `:<input>`, resolves alias → GVR, instantiates the viewer via `registrar`, pushes onto the stack.
- Powers the suggestion engine by exposing completions over the alias namespace.

---

## 5. Rendering

### Renderer pattern

Each resource has a renderer in `internal/render/` (e.g. `pod.go`, `deploy.go`) implementing `Columner`:

```go
type Columner interface {
    Header([]string) model1.HeaderColumn
    Rows(ctx context.Context, ns string, gvr *client.GVR, re model1.RowEvent) ([][]string, error)
}
```

Row color comes from a `ColorerFunc` attached to the table (`internal/ui/table.go:28`). Example: a pod row is red on error, yellow on pending, green when running. Default colorer lives in `model1.DefaultColorer`; renderers can override.

### Columns

- Headers defined by the renderer.
- User overrides live in `~/.config/k9s/views.yaml`.
- Sort column chosen with Left/Right arrows; `manualSort` flag prevents auto-reshuffle when the watcher fires.

### Age and status

- Ages rendered as human deltas ("2h 30m").
- Status colors come from the skin (`frame.status.{newColor,modifyColor,errorColor,…}`).
- Resource icons optional (`k9s.ui.noIcons`).

---

## 6. Config and theming

### Config locations (XDG)

- `~/.config/k9s/config.yaml` — global.
- `~/.config/k9s/clusters/<cluster>/<context>/k9s.yaml` — per-context overrides; written with Ctrl-P.
- `~/.config/k9s/aliases.yaml` — shared alias map. Loaded in `internal/config/alias.go:132-199`.
- `~/.config/k9s/hotkeys.yaml` — user-defined key bindings, can override defaults.
- `~/.config/k9s/views.yaml` — per-resource column settings.
- `~/.config/k9s/plugins.yaml` — custom commands bound to keys.
- `~/.config/k9s/skins/*.yaml` — user themes; bundled themes in the repo under `skins/`.

### Skin schema

See `internal/config/styles.go:51-180`. Abbreviated:

```yaml
k9s:
  body: { fgColor, bgColor, logoColor }
  prompt: { fgColor, bgColor, suggestColor }
  frame:
    border: { fgColor, focusColor }
    menu: { fgColor, keyColor, numKeyColor }
    crumbs: { fgColor, bgColor, activeColor }
    title: { fgColor, highlightColor, counterColor }
    status: { newColor, modifyColor, errorColor, addColor, pendingColor, broken }
  views:
    table: { cursorFgColor, cursorBgColor, markColor, header: {...} }
    xray: { bgColor, graphicColor }
    yaml: { keyColor, colonColor, valueColor }
    logs: { fgColor, bgColor, indicator: {...} }
```

36 bundled themes (dracula, gruvbox, solarized, …). Active skin picked via `k9s.skin`. Skin reload is **live** — the styles object exposes `AddListener`, every widget re-paints on `StylesChanged()`.

### Plugins

Plugins are YAML-defined: `command`, `args`, `shortCut`, `description`, `scopes` (which views show the plugin). Bound into `KeyActions` so they render in the menu hints.

---

## 7. Model and data layer

### Table model

- `internal/model/table.go:40-100` wraps data in `model1.TableData` (header + rows).
- Default refresh 2 s, configurable (`k9s.refreshRate`).
- Updates debounced (300 ms initial, 2 s steady-state) to avoid UI churn.

### Listener pattern

- `TableListener`: `TableDataChanged`, `TableLoadFailed`.
- Fired from background goroutines; UI wraps each in `QueueUpdateDraw`.

### Command history

- `app.cmdHistory` (max 100) and `app.filterHistory` feed the suggestion engine when the buffer is empty, so hitting `:` + Up/Down walks recent commands.

### FishBuff

`internal/model/fish_buff.go` — generic enough to lift wholesale. Exposes `SetSuggestionFn(func(prefix string) []string)`; the table/pages views register namespace and alias suggesters into it.

### Watchers

Kubernetes specifics — the relevant takeaway is that k9s keeps a `watch.Factory` that the views subscribe to, decoupling data acquisition from presentation. Our analog in `a10r` will poll Alertmanager's HTTP API (or use `/api/v2/alerts` with long-ish intervals).

---

## 8. Concurrency model

### `QueueUpdateDraw`

```go
// internal/ui/app.go:64-82
func (a *App) QueueUpdateDraw(f func()) {
    if a.Application == nil {
        return
    }
    go func() {
        a.Application.QueueUpdateDraw(f)
    }()
}
```

Every UI mutation from a background goroutine goes through this path. tview serializes draw calls.

### Context cancellation

Each view owns a `context.CancelFunc`. Closing the view cancels the context, which tears down watchers. See `internal/view/pulse.go:88`, `internal/view/xray.go:46`.

### Locking

- `sync.RWMutex` on mutable table state and action registry.
- `atomic.Int32` for hot flags (`inUpdate`, `conRetry`).
- Channels for flash messages (`model.FlashChan`).

---

## 9. Startup flow

```
main.go:44              cmd.Execute()
cmd/root.go:41          root cobra command
cmd/root.go:76-128      load config → k8s client → view.NewApp(cfg)
internal/view/app.go:95-144
                        a.App.Init()
                        SetInputCapture(a.keyboard)
                        build factory (watchers)
                        load aliases
                        create splash
                        build flex layout
                        reload styles
app.Run()               show splash 1 s → defaultCmd() → first view
```

First view is `k9s.defaultView` if set, otherwise `pods`, otherwise `context` if the cluster is unreachable.

---

## 10. What makes k9s *feel* like k9s

Distilled, in priority order for a10r:

1. **Single-key, keyboard-first operation.** No command needs the mouse. Every action is discoverable via the menu hints.
2. **`:` command bar with fish-style suggestions.** Fast resource switching, low cognitive load.
3. **Stack navigation with ESC = back.** Drilling from list → detail → sub-resource feels like a browser.
4. **Live `/` filter** on every table.
5. **Breadcrumbs that reflect the stack.** You always know where you are.
6. **Dynamic menu hints** rendered from the action registry rather than hand-maintained help text.
7. **Flash messages** for ephemeral feedback — success/warn/error with a consistent emoji prefix.
8. **Colored, sortable tables** with row colors tied to domain status.
9. **Themable via YAML skins** with live reload.
10. **Read-only mode** as a first-class config flag.
11. **Specialty modes** for things tables cannot express: **Pulse** (real-time sparkline grid), **Xray** (relationship tree), **Logs** (tailing). Each one is a legitimate alternative view, not a bolted-on feature.

---

## 11. Mapping to a10r (Alertmanager)

Direct analogs we expect to build:

| k9s concept | a10r analog |
|---|---|
| Cluster context | Alertmanager endpoint / cluster |
| Namespace | Alert group or receiver (TBD) |
| Pod (default view) | Alert |
| Deployment / service | Alert group |
| Events | Notification log / silences history |
| Xray tree | Alert → labels → silences/inhibitions |
| Pulse | Firing-rate sparklines per receiver/severity |
| Describe/Edit | View alert JSON, open/edit silences |
| Logs | Recent notification attempts for a receiver |
| `:ctx` | `:am` to switch Alertmanager endpoint |
| `:ns` | `:recv` or `:group` |
| Read-only mode | First-class; silence/ack disabled |

### Concrete design bets we should adopt now

1. **Bubbletea + bubbles + lipgloss as the TUI stack.** Forks only on concrete pain. Decision and trade-offs in `docs/design/tui-library-comparison.md`.
2. **Lift the FishBuff *idea*, not the file.** k9s's `internal/model/fish_buff.go` is tview-coupled. We keep the ring-buffer + arrow-key cycler concept and pair it with `bubbles/textinput` for input and a small lipgloss-rendered strip for inline suggestions.
3. **Reuse the page stack / crumbs / menu triad.** It is what makes k9s feel navigable. Implement as a small bubbletea sub-model that owns the stack and broadcasts push/pop messages to the crumb and menu components.
4. **YAML config with schema validation from day one.** Avoid a reshuffle later.
5. **Skin system.** Bundled themes embedded in the binary; user themes loaded from `<config-dir>/skins/`. Concrete schema, bundle list, and v0.1 cuts (live reload deferred, adaptive light/dark skipped) live in `docs/design/theming.md` (open-question M1).
6. **Action registry with `Dangerous` flag** so read-only mode is a one-line toggle.
7. **`Program.Send(msg)` from poller goroutines** instead of UI mutation. Bubbletea's update loop serializes messages; no `QueueUpdateDraw` analog needed.
8. **Per-view `context.CancelFunc`** to stop background work on ESC.

### v0.1 polish — UX rules learned by testing

These rules emerged after the first wave of user testing against the Prometheus public demo and a 2-tenant production config. They're listed here (and in `keybindings.md` for the parts that touch input) so the next contributor doesn't relearn them by stumbling on the same regressions:

- **Top panel is k9s 3-column.** Left: tenant numeric quick-switch listing. Middle: page-specific verbs (Bindings()). Right: ASCII A10r logo. The labelled info block (`tenants:`/`alerts:`/`version:`) k9s carries was dropped — the body title already renders `alerts(scope)[count]`, so a parallel block in the chrome was duplicate noise. Each row has its own gap-elision logic so a narrow terminal degrades gracefully rather than overlapping columns.
- **ASCII logo is figlet "standard" for `A10r`** (mixed case, not lowercase). Multi-line; pad each line to the longest line's width before composition so per-row right-alignment doesn't stagger.
- **Body sits inside a bordered panel.** `┌── <Title> ──┐ … └────┘`. The title carries `<resource>(<scope>)[<count>]`. Subtitle (if any) sits one line below the title before the body proper.
- **Breadcrumbs are bold and wrapped: `<crumb>`.** Top-of-stack is bolded the same as the rest; what differentiates the active crumb is its position, not extra emphasis.
- **Cursor highlight uses a *bright* background (lavender per default mocha skin), not a subtle grey.** k9s's default surface1 felt invisible against our body bg. Cursor row gets full fg+bg; marked rows get fg-only tint (rosewater on body bg).
- **Column header row is foreground-only.** Theme.Table.Header foreground over the body's background — no header stripe. Uppercase labels. Sort arrow `↑`/`↓` on the active column is the source of truth — do not also display `sort:column ↑` as a subtitle.
- **Numeric tenant quick-switch is a global LayerGlobal binding.** Per-page handlers for digits would be dead code (the dispatcher consumes them before forwardToTop). Pages observe `app.ScopeChangedMsg` if they need to rescope (alerts page does; the others ignore it for now).
- **`<?> help` is a global, not per-page.** The right-hand hint column is for view-specific verbs only.
- **One poller per backend.** `cmd/tui.go:startBackendPoller` fans over every `cfg.Backends`. Each poller emits `poll.DataMsg{Tenant: be.Name}`. List pages keep a `byTenant map[string][]T` and union it through `scopeIncludes` so the `[N]` count, the visible rows, and the optional `TENANT` column all derive from one place.
- **TENANT column appears iff scope=="all" AND len(byTenant) > 1.** Single-tenant scope hides it even when other tenants are still in the byTenant cache.
- **Wrap long annotation values with hanging-indent + forward-progress guard.** `wrapHanging` must hard-cut when the next break point lies *before* the indent column, otherwise the loop hangs on values with no internal whitespace.

### Things to reconsider (do better than k9s)

- **Vim motions.** k9s only wires arrows/PgUp/PgDn. Adding `j`/`k`/`gg`/`G`/`Ctrl-D`/`Ctrl-U` is cheap and our target audience expects it.
- **Mouse support is uneven.** Make it consistent or turn it off by default.
- **Slash command discoverability.** k9s hides aliases behind the `:alias` view; we could surface them in the prompt's idle suggestions.
- **Config file sprawl.** k9s has five+ YAMLs. One file with sections is usually fine for a10r's scope.

---

## 12. File index for future reference

Cheat-sheet of the files to open when working on a10r and you want to check how k9s does it:

| Topic | File |
|---|---|
| App lifecycle, keyboard capture | `internal/ui/app.go`, `internal/view/app.go` |
| Layout flex | `internal/view/app.go:166-183` |
| Pages / stack | `internal/ui/pages.go` |
| Tables | `internal/ui/table.go`, `internal/view/table.go` |
| Action registry | `internal/ui/action.go` |
| Key constants | `internal/ui/key.go` |
| Menu hints | `internal/ui/menu.go` |
| Crumbs | `internal/ui/crumbs.go` |
| Prompt + suggestions | `internal/ui/prompt.go`, `internal/model/fish_buff.go` |
| Flash | `internal/ui/flash.go` |
| Splash | `internal/ui/splash.go` |
| Indicator | `internal/ui/indicator.go` |
| View registrar | `internal/view/registrar.go` |
| Command resolver | `internal/view/command.go` |
| Renderer interface | `internal/render/*.go` |
| Skin schema | `internal/config/styles.go`, `skins/*.yaml` |
| Aliases | `internal/config/alias.go`, `~/.config/k9s/aliases.yaml` |
| Config entry | `internal/config/k9s.go`, `cmd/root.go` |
| Pulse | `internal/view/pulse.go` |
| Xray | `internal/view/xray.go` |
| Logs | `internal/view/log.go` |

---

## 13. Open questions for a10r

Tracked in `docs/design/open-questions.md`. The questions raised by this audit (Pulse-from-day-one, silence-as-top-level, per-context config layout, sort indicators, caching) live there alongside questions raised by the backend audit and the bubbletea decision.
