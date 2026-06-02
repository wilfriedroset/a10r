# 0024 — `internal/listcmd` Pipeline orchestrator for headless list commands

The four headless list commands (`a10r alerts list`, `silences list`,
`groups list`, `receivers list`) each carry the same 5-stage
orchestration spine: parse `--output`, load config, build the
`--debug-http` logger, fan out one `c.ListX(ctx, …)` call per
configured backend, render the result via a TTY-aware pager and
return one of three exit codes (success, ExitUnreachable when every
backend failed, ExitFailMatched when `--fail` survived a non-empty
row slice). The bodies diverged on ~50 LOC of identical boilerplate
per command — same fetch loop, same partial-failure aggregation,
same pager dance, same `--fail` epilogue — guarded by per-command
filter slabs that only differ in which struct field they read. The
duplication had already started drifting: the silences fetcher
captured the matcher predicate inside its loop, the alerts fetcher
ran the filter post-loop; both were correct but reviewers had to
re-derive "this is the same shape with a different filter".

This ADR records the decision to **extract the shared orchestration
into `internal/listcmd`**. The package exposes a single entry point
— `Run[R any](ctx, spec Spec[R]) error` — and three contract types:
`Spec[R]` (the per-run input), `Renderer[R]` (the format-specific
writer), and `Deps` (the construction-time seam). Per-command
filter logic moves *into* the `Fetcher` closure each command builds;
the pipeline never sees an unfiltered slice, so the filter is a
private detail of the command rather than a knob on a shared API.

Generic over `R` is the deliberate choice: each command's row type
already exists (`alertRow`, `silenceRow`, `groupRow`, `receiverRow`)
with command-specific JSON/YAML tags, and forcing them onto a
shared row schema would re-introduce the matcherRow-vs-receiverRow
question every renderer has to answer differently anyway. The
pipeline's interaction with `R` is purely structural — call
`Spec.Fetcher` to get `[]R`, call `Spec.Sort([]R)` to order it,
call `Spec.Renderers[format](w, []R)` to write it — so the type
parameter pays for itself by removing every `any` cast from the
body.

**Fan-out is parallel via `errgroup` with deterministic output
ordering.** Today's fetch loop runs sequentially; with N tenants
the wall-clock time is the sum of per-tenant latencies. The
pipeline runs each `Spec.Fetcher` in its own goroutine via
`errgroup.WithContext` so a slow tenant no longer blocks the
others. Output stability is preserved by sorting both the per-
backend error slice and the accumulated row slice *after* the
goroutines join: errors sort by their `backend %q:` string prefix
(the natural backend-name order), rows sort by the per-command
`Spec.Sort` callback the cmd layer already supplied. The
load-bearing property is "two runs against the same fixture print
the same bytes regardless of which goroutine the scheduler picked
first" — the parallelism is a latency win, not a correctness
hazard. Every existing CLI test that asserted stderr line order or
row order continues to pass because the previous serial order also
happened to be alphabetical by backend name in every fixture; the
new alphabetical sort converges on the existing expectations.

**Exit-code mapping stays in cmd/.** The pipeline returns
*canonical sentinel errors* — `ErrAllBackendsFailed` and
`ErrMatched` — wrapped with a templated message
("--fail: N <label>(s) matched the filter") that carries the
per-command count and `Spec.ResourceLabel`. cmd/ matches via
`errors.Is` and routes the sentinels to
`ExitFailMatched` / `ExitUnreachable`; `ExitConfigInvalid` stays at
the `cmd/listcmd.go:loadCmdConfig` seam because the pipeline does
not load config. This split keeps the package free of cmd's exit
table (ADR 0009 lives in `cmd/exit.go`) while keeping the printed
error messages byte-identical to today's strings — the cmd-side
wrappers used the same `--fail: N X(s) matched the filter`
template, just inlined.

**Pager is injected via `Spec.Deps.PagerFactory`.** Production
wires a one-liner over `cmd.NewPager`; tests inject a write-through
fake. The signature matches `cmd.NewPager` byte-for-byte so the
wrapper is a forwarding call. Zero-value `Deps` resolves to a
write-through factory so the pipeline can be tested without a TTY,
matching the `internal/tui/boot.Deps` idiom that ADR 0023 just
landed.

Considered and rejected: (a) **non-generic `Pipeline` over
`[]any`** — every renderer would type-assert back to its concrete
row type, re-introducing the runtime cast the type parameter
eliminates; the JSON/YAML encoders need the static type to honour
the struct tags, so an `any` slice would just push the type
assertion one stack frame inward; (b) **filter as a `Spec`
function** (`Filter func([]R) []R` alongside `Sort`) — splits the
per-command knowledge into two fields when one closure (the
`Fetcher`) already has both the per-tenant context and the filter
state from the parent options struct; the closure shape keeps
`Spec` minimal and stops the pipeline from ever owning an
unfiltered slice; (c) **per-command Pipelines** (`alerts.Pipeline`,
`silences.Pipeline` …) — would duplicate the fan-out / pager /
exit-code glue across packages and re-introduce drift; the shared
orchestrator is the entire point; (d) **single-threaded fan-out
preserved** — keeps today's exact behaviour but doesn't pay back
the extraction cost; the only behavioural change in this ADR is
the parallelism, and it's a strict latency improvement protected
by the post-join sort; (e) **expose the cmd `NewPager` via an
interface the pipeline imports** — would require defining the
interface in the pipeline package and having cmd implement it,
inverting the dependency direction; the `PagerFactory` function-
typed seam keeps the cmd→listcmd direction one-way and matches
the Deps idiom from `internal/tui/boot`; (f) **return the
deterministic-order property as a comment on `Run`** instead of
a post-join sort — the sort is the property; relying on
errgroup's scheduling order would make the test suite flaky on
the first GOMAXPROCS change, and the comment that promises
determinism would silently lie.
