# 0026 — pagetest fixture builders and style cache

The four largest page tests (`silences_test.go` ~2099 LOC,
`alerts_test.go` ~2055 LOC, `alert_test.go` ~1056 LOC,
`groups_test.go` ~596 LOC) plus the supporting orchestration tests
total ~7000 LOC of which a load-bearing slice is identical
scaffolding: every test loads `*theme.Styles` via
`testutil.LoadStyles` (parsing the embedded skin YAML afresh on
every call), and each file re-implements the same domain-value
fixture builders — `mkAlert(name, severity, state) backend.Alert`,
`sil(id, by, state, endsIn) backend.Silence`, `mkGroup` /
`sampleGroups` — with subtly different default sets (severity
defaults to `"warning"` in one file but `"critical"` in another;
`StartsAt = now - 1m` for one, `now - 5m` for another). The drift
is real: the alerts file's `mkAlert` and the alert-detail file's
`sample()` produce alerts with different fingerprints, severities,
and annotation maps, so a reader has to context-switch on every
file open.

This ADR records the decision to **introduce `internal/tui/page/
pagetest`** — a sibling of `listpage`, `detailpage`, `cursor`,
`format` — to hold the absorbed fixture builders
(`pagetest.Alert(AlertOptions)`, `pagetest.Silence(SilenceOptions)`,
`pagetest.Group(GroupOptions)`) and a `sync.Once`-cached
`Styles(t) *theme.Styles` so the first test in a test package pays
the embedded-YAML parse and every subsequent caller borrows the
cached pointer. Migration is file-by-file with one commit per
migrated test file so each step is independently reviewable and
`git bisect`-safe.

The package must NOT import any page package (`silences`,
`alerts`, ...) because the page packages already import `testutil`,
and threading a reverse dependency through `pagetest` would create
the kind of test-only package-cycle pain the existing `listpage`
rule-of-three was meant to prevent. The cost is that each migrated
test still writes its own `silences.New(silences.Options{Styles:
pagetest.Styles(t), ...})` constructor call. The win is shared
fixture defaults, consistent timestamps across pages, and the
one-parse Styles cache.

Fixture builders use the option-struct constructor shape (matching
the existing `silences.Options` / `alerts.Options` idiom) rather
than fluent chains. Round 1 of the design grill locked this: option
structs are trivially zero-valued, every override is named at the
call site, and there's no `.WithFoo()` method surface to maintain.
Each builder has sensible defaults so a zero-value Options still
produces a renderable value — `pagetest.Alert(pagetest.AlertOptions
{})` returns an Alert with `alertname=TestAlert`,
`severity=warning`, state Active, and `StartsAt` one minute before
the package's `defaultNow`. Override semantics are explicit: `Now`
defaults to `2026-04-25 12:00 UTC` (the historical `fixedNow`
constant) so migrated tests get bit-for-bit identical timestamps
without restating the date.

Considered and rejected: **(a)** a generic `Harness` over
`app.Page` that absorbs the `Update` / `View` + `Strip` lifecycle —
considered and built during the first implementation pass, then
removed during review when no migrated test ended up using it. The
existing test bodies heavily access concrete `*Page` internals
(`p.view[0].s.ID`, `p.pendingEdit`, `p.flat`, `p.expanded`, ...) to
verify state transitions the rendered string can't observe;
forcing every test through `h.Page().(*Page).view[0]` was a
readability loss with no callers in sight. The fixture builders and
style cache deliver the structural wins the grilled design asked
for; a lifecycle wrapper without consumers would have been
speculative abstraction in violation of CLAUDE.md's
"don't design for hypothetical future requirements." If a new
black-box lifecycle test arrives later, the wrapper goes in then;
**(b)** per-page constructor wrappers
(`pagetest.NewSilencesPage(t, opts)`) — would force `pagetest` to
import every page package, creating the test-only cycle the package
boundary is meant to avoid; **(c)** a fluent-chain builder API
(`pagetest.NewAlert().WithSeverity("critical").Build()`) — adds a
method surface that has to grow in lockstep with the fixture
fields, costs a chain-method per option, and reads poorly when half
the call sites override one or two fields and leave ten at default;
**(d)** an assertion DSL
(`h.AssertView(t).Contains("...").NotContains("...")`) — would
have one vocabulary across pages, but the existing
`require.Contains(out, ...)` is already short and the DSL would
replace one stdlib import with a per-page abstraction; **(e)** a
single big-bang commit migrating every file at once — would make
the diff unreviewable, break `git bisect` across the migration
window, and conflate fixture-design issues with per-file
translation bugs; the chosen one-commit-per-file shape lets a
reviewer reject a problematic migration without rolling back the
package itself; **(f)** caching `*theme.Styles` inside
`testutil.LoadStyles` instead of in pagetest — would change the
contract of an existing helper called from non-page tests too
(loader_test.go etc.), and tests that need a fresh loader for a
regression repro would have to bypass the cache; the new cache
lives in pagetest specifically because it's a page-test
optimisation, and the embedded-assets exemption in CLAUDE.md covers
exactly one package-level `sync.Once` here.
