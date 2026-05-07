# Fuzzing a10r

## Goal

Pre-1.0 hygiene: lock in "no panic from any reachable user input"
across the bubbletea stack before tagging a release. No specific
incident motivates this — the bubbletea Update functions are the
densest-but-least-tested surface in the repo, and `testing/F`
gives us a coverage-guided regression net for nearly free.

## Decision: Go-native `testing/F`

Two fuzz targets, both panic-only (call `Update` then `View`,
fail iff either panics). No logical invariants — they introduce
noise and triage overhead that does not pay back at the hygiene
tier.

### Targets

| Target | File | Scope |
|---|---|---|
| `FuzzApp` | `internal/tui/app/fuzz_test.go` | Top-level app with the alerts page pushed. Catches cross-page bugs and chrome (header, footer, modal, prompt, dispatcher). |
| `FuzzSilenceForm` | `internal/tui/form/silence/fuzz_test.go` | Densest state machine in the repo (375 LOC, six fields with validation + duration parsing). Top-level fuzzer rarely drives random keys deep into a multi-field form; a focused target earns its keep. |

A pure top-level fuzzer would miss the form's deep states. A
per-page fuzzer for every page is over-engineering for a pet
project — the list pages are generic enough that the app-level
target reaches them naturally.

### Input encoding

`testing/F` feeds bytes; `tea.Msg` is an interface. Encoding maps
a byte stream to a `[]tea.Msg`:

- byte 0 mod 16 picks the message type:
  - 0..13 → `tea.KeyPressMsg` (rune from the next byte)
  - 14    → `tea.WindowSizeMsg` (next two bytes → W, H clamped to 0..500)
  - 15    → no-op (allows the corpus to express idle frames)

Paste, mouse, and synthesised internal messages are deliberately
out of scope. Paste was considered for the form fuzzer; dropped
to keep one encoding shared between the two targets. Synthesised
internal messages (fake `alertsLoadedMsg`, etc.) are excluded
because they generate panics that no real user could reach —
which is exactly the triage trap dropping Bombadil avoided.

### Backend

`FuzzApp` constructs the alerts page wired to a small fake
satisfying `internal/tui/form/silence.Client` (two methods —
`CreateSilence`, `UpdateSilence`). Page reads flow through
`poll.DataMsg` rather than the backend client, so a full
14-method `backend.Client` fake is unnecessary and is not built.

`FuzzSilenceForm` uses the same fake plus a fixed synthetic
matcher prefill.

### Seeds

`f.Add(...)` calls in each fuzz file. Crash repros land in
`testdata/fuzz/<target>/` and are checked in — they become
regression tests on every `go test ./...`.

`FuzzApp` seeds drive the app into a distinct state per seed:
empty, resize-only (including 0×0, 1×1, 500×500), vim
navigation, modal cycles (`?`/Esc, `:`/Esc), filter prompt
(`/foo<Enter>`), and the silence-creation flow.

`FuzzSilenceForm` seeds: empty form, Tab through every field,
realistic matchers (`alertname=foo`, `severity=~"warning|critical"`),
realistic durations (`1h`, `30m`, `2h30m`), and edge inputs
(empty matcher, very long matcher, unicode comments,
control-char comments).

## Run frequency

- **Floor (every commit).** `go test ./...` runs every seed and
  every checked-in repro as ordinary subtests — zero new CI
  cost, regressions stick.
- **Explore (nightly).** `.github/workflows/fuzz.yml` runs both
  targets with `-fuzztime=5m`, uploads `testdata/fuzz/` on
  failure as an artifact for triage.
- **Local.** `make fuzz` wraps the canonical command for
  contributors.

`-race` is intentionally off during fuzz: it slows iteration ~10×
and the race surface is already covered by `make test-race`.

## Alternatives considered

### Bombadil terminal fuzzer

`bombadil-terminal` ([wickstrom.tech 2026-04-30](https://wickstrom.tech/2026-04-30-bombadil-terminal-experiment.html))
is a black-box TUI fuzzer (Rust + Zig) that drives a binary
through a PTY and inspects the rendered output via a terminal
emulator. Language-agnostic, so it would work against `a10r`
unchanged.

Not adopted for the following reasons:

1. **Pre-release toolchain.** Only CI artifacts today, no tagged
   release. Pinning a CI-artifact SHA pulls a Rust+Zig stack into
   our pipeline for ongoing maintenance — too much overhead for a
   pet project with no fork-maintenance appetite (CLAUDE.md
   "no forks").
2. **Marginal coverage over `testing/F`.** Bombadil's edge is
   render-path crashes (ANSI emission, terminal-emulator state).
   We do not author ANSI directly — render output comes from
   bubbletea + lipgloss. Render-path panics that originate in
   `a10r` code mostly *also* surface as Go-level panics that
   `testing/F` catches. The narrow remaining category is not
   obviously worth the toolchain cost.
3. **Shallow input model.** Random ASCII + ANSI + resize. Our
   modal keys (`?`, `:`, `e`, `Ctrl+T`, `Ctrl+X`) would be
   stumbled into stochastically; the silence form, filter
   pipeline, and tenant switch would barely be exercised.
4. **No oracle.** Crashes and hangs only, unless we layer
   invariants — which we explicitly chose not to do.

Reconsider iff (a) Bombadil tags a stable release and (b) we hit
a render-path panic that `testing/F` could not have caught.

### Logical invariants on top of crash detection

E.g. `View()` non-empty for any non-zero size, selected index in
range, modal stack bounded, help-key always closes help. Each
invariant is also a thing that can be wrong and produce false
positives. Logical invariants pay back when motivated by a
specific bug class; pre-1.0 hygiene with no incident behind it
does not motivate them. Add them later as regression assertions,
not as fuzz-time checks.

### Snapshot/golden render diff

Heavy, mostly catches *intentional* visual changes. Wrong tool
for hygiene; out of scope.

## Open follow-ups

- If the form fuzzer surfaces panics in `parseMatchers` /
  duration parsing specifically, factor those into table-driven
  unit tests — fuzz finds the crashes, unit tests pin the fix.
- Track the time `FuzzApp` spends on the home page vs deeper
  states. If exploration plateaus on the alerts list, extend the
  seed corpus with more "drill into detail page" sequences
  rather than per-page fuzzers.
