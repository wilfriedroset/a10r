# 0026 — pagetest harness for page-test lifecycle scaffolding

The four largest page tests (`silences_test.go` ~2099 LOC,
`alerts_test.go` ~2055 LOC, `alert_test.go` ~1056 LOC,
`groups_test.go` ~596 LOC) plus the supporting orchestration tests
total ~7000 LOC of which a load-bearing slice is identical
scaffolding: load `*theme.Styles` via `testutil.LoadStyles`, build
the page's `Options{}` literal, call `New(...)`, drive `Update(msg)`
sequences while discarding the returned `app.Page` and (often) the
`tea.Cmd`, then `testutil.StripStyle(p.View(w, h))` before string-
matching. Each file also re-implements the same domain-value
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
`format` — to hold a `Harness` over `app.Page`, the absorbed
fixture builders (`pagetest.Alert(AlertOptions)`,
`pagetest.Silence(SilenceOptions)`, `pagetest.Group(GroupOptions)`),
and a `sync.Once`-cached `Styles(t) *theme.Styles` so the first
test in a test package pays the embedded-YAML parse and every
subsequent caller borrows the cached pointer. Migration is
file-by-file with one commit per migrated test file so each step
is independently reviewable and `git bisect`-safe.

The harness operates on `app.Page`, not on any concrete page type.
That choice is load-bearing: `pagetest` must NOT import any page
package (`silences`, `alerts`, ...) because the page packages
already import `testutil`, and threading a reverse dependency
through `pagetest` would create the kind of test-only package-
cycle pain the existing `listpage` rule-of-three was meant to
prevent. The cost is that each migrated test still writes its own
`silences.New(silences.Options{Styles: pagetest.Styles(t), ...})`
constructor call. The win is that the rest of the lifecycle —
`Update`, `View`+`Strip`, fixture builders — lives in one place.

The harness's `Update(msg tea.Msg) tea.Cmd` returns the `Cmd`
because a meaningful slice of the existing tests capture it
(silences: 34 sites, alerts: 14, alert: 17, groups: 7) to assert
on emitted messages. Tests that don't care use `Send(msg)`, the
no-return-value sibling. `Update` re-assigns the tracked
`app.Page` from the call's first return so future `View` calls
see the replacement — today every production page returns
`(p, ...)` from its `Update` so the threading is a no-op, but the
interface contract permits replacement and the harness is correct
either way.

Fixture builders use the option-struct constructor shape (matching
the existing `silences.Options` / `alerts.Options` idiom) rather
than fluent chains. Round 1 locked this: option structs are
trivially zero-valued, every override is named at the call site,
and there's no `.WithFoo()` method surface to maintain. Each
builder has sensible defaults so a zero-value Options still
produces a renderable value — `pagetest.Alert(pagetest.AlertOptions
{})` returns an Alert with `alertname=TestAlert`,
`severity=warning`, state Active, and `StartsAt` one minute before
the package's `defaultNow`. Override semantics are explicit:
`Now` defaults to `2026-04-25 12:00 UTC` (the historical `fixedNow`
constant) so migrated tests get bit-for-bit identical timestamps
without restating the date.

Render assertions stay mechanical: `Harness.View(w, h)` returns the
stripped string and tests use stdlib `strings.Contains` /
`require.Contains`. No assertion DSL is introduced — the existing
substring-matching shape works, and a DSL would lock the harness
to one assertion vocabulary across pages whose render contracts
differ.

Considered and rejected: **(a)** per-page constructor wrappers
(`pagetest.NewSilencesPage(t, opts) *Harness`) — would force
`pagetest` to import every page package, creating the test-only
cycle the package boundary is meant to avoid; the page-construction
call is also the one line per-file that genuinely benefits from
staying inline because its option set encodes per-test intent
(read-only mode, custom Tenants, fake clients) the harness has no
business absorbing; **(b)** a fluent-chain builder API
(`pagetest.NewAlert().WithSeverity("critical").Build()`) — adds a
method surface that has to grow in lockstep with the fixture
fields, costs a chain-method per option, and lipgloss-style chains
read poorly when half the call sites override one or two fields
and leave ten at default; option structs flatten that to one
struct literal per call; **(c)** an assertion DSL
(`h.AssertView(t).Contains("...").NotContains("...")`) — would
have one true vocabulary across pages, but the existing
`require.Contains(out, ...)` shape is already short and every test
package already imports testify; the DSL would replace one stdlib
import with a per-page abstraction that has to be updated as
testify evolves; **(d)** a single big-bang commit migrating every
file at once — would make the diff unreviewable, break `git bisect`
across the migration window, and conflate harness-design issues
with per-file translation bugs; the chosen one-commit-per-file
shape lets a reviewer reject a problematic migration without
rolling back the harness itself; **(e)** caching `*theme.Styles`
inside `testutil.LoadStyles` instead of in pagetest — would change
the contract of an existing helper that's called from non-page
tests too (loader_test.go etc.), and tests that need a fresh
loader for a regression repro would have to bypass the cache; the
new cache lives in pagetest specifically because it's a page-test
optimisation, and the embedded-assets exemption in CLAUDE.md
covers exactly one package-level `sync.Once` here.
