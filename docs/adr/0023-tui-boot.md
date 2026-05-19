# 0023 — TUI startup orchestrator extracted to `internal/tui/boot`

`cmd/tui.go` carried ~28 helpers and a ~250-line `runTUI` whose
body sequenced them in a precondition order encoded only by call
sequence: logger built before client factories so per-request
debug logging is wired before any HTTP call; version-fetch
before tenant page caches so the row list ships its VERSION
column on first render; dispatcher chord registration before
`app.NewApp` so the App's pre-built layers see `gg` / `Ctrl+\`
on the first key event; user keybinding overrides loaded *after*
`app.NewApp` so `ApplyOverrides` finds every built-in action.
Adding a stage or moving one is a re-derivation exercise — the
implicit graph is in the helper names and the ordering of their
calls. The cmd-layer review that triggered ADR 0022's PageEnv
bundling left the same observation against `runTUI` itself: the
file had become a single-function module too big to review as a
unit.

This ADR records the decision to **extract the TUI startup graph
into `internal/tui/boot`**. The package exposes `Build(ctx,
flags, deps) (*Result, error)` whose body is a sequential list of
numbered stages, one block comment per stage stating the
load-bearing precondition. Helpers move next to the stage that
calls them (`logger.go`, `clients.go`, `tenants.go`,
`transport.go`, `resolver.go`, `theme.go`, `config.go`,
`poller.go`, `cmdbar_args.go`, `page_factory.go`,
`quitfilter.go`) so a contributor scanning Build sees the
orchestration without paging through helper noise.

`Result` is the post-Build handle the wiring layer uses to
finish startup. The bubbletea `*tea.Program` is created *after*
Build returns — in `cmd/tui.go`, which is the only caller that
ever has a live program. The poller goroutines and the home-page
push both need `prog.Send`, which doesn't exist until
`tea.NewProgram` runs, so they're driven via `Result.StartPollers`
and `Result.PushHome` rather than smuggled inside Build. `Result`
embeds the unexported registry / clients / env captured at Build
time so those two methods need no more arguments than the
program's send func. `Result.Close()` flushes the logger sink
and satisfies `io.Closer` so `cmd/tui.go` can `defer res.Close()`
cleanly.

`Deps` is the construction-time seam every test exercises. Zero
value is valid — `Deps.resolved` fills in production constructors
(config.Load, a10rlog.New, factory.Build, theme.Loader.Load,
config.LoadAliases, config.LoadKeys, config.ResolveDir,
edit.SystemResolver, footer.DefaultHistoryDir) so a test that
needs to short-circuit only one of those passes a populated
`Deps` for that field and leaves every other at zero. Build's
own body never branches on which fields were defaulted; the
defaulting is concentrated in one method so the stage sequence
reads with intent. The pre-boot cmd helpers `levelFor`,
`userAgent`, and `loadOptsFromFlags` are exported as
`boot.LevelFor` / `boot.UserAgent` / `boot.LoadOptsFromFlags`
because every non-TUI `a10r alerts list` / `silences list` /
`groups list` / `receivers list` / `doctor` / `info` /
`validate` subcommand calls them too; the cmd-side `buildmeta.go`
keeps the lowercase wrappers so the call sites in those
subcommands stay unchanged.

`cmd/tui.go` shrinks to ~55 LOC: parse flags, call
`boot.Build`, defer Close, wrap the App in `tea.NewProgram` with
`boot.QuitFilter`, start the pollers, push home, block on
`prog.Run`. The SIGTERM-cascade contract (raw `tea.QuitMsg` /
`tea.InterruptMsg` translates to `app.QuitRequestedMsg` so the
page-stack Close runs before bubbletea tears down) lives in
`boot.QuitFilter` so a future signal-handling deepening has one
place to touch.

Considered and rejected: (a) absorb the boot graph into
`internal/tui/app` — App is the bubbletea Model that the program
loop owns; widening its surface to include factory wiring would
force the App to import `backend`, `factory`, `config`, `theme`,
the page packages, every cmdbar handler, and the poller, which
inverts the dependency direction the package boundary is meant
to enforce; (b) replace `Deps` with functional options
(`boot.WithLogger(...)`, `boot.WithClientFactory(...)`) — adds a
constructor surface that grows linearly with every seam, while
the struct's zero-value defaulting reads exactly the same in
production and lets tests assign fields by name; functional
options pay off when the option set is open-ended, but the boot
seams are a closed, audit-driven set; (c) typed stage values
(`boot.Stage1Result`, `boot.Stage2Result`, … threaded through
`Build`) — the data flow between stages is genuinely linear, so
named carrier structs would just be local variables in disguise;
the readability win was supposed to be "the stage graph is
visible", but the same property falls out of the block-commented
sequential body in `Build` itself; (d) `Build(ctx, flags, deps)
(*app.App, io.Closer, error)` exactly per the original locked
return shape, with the post-program wiring re-implemented in
`cmd/tui.go` — works, but every test of `StartPollers` and
`PushHome` would have to assemble the App / deps / env scaffolding
by hand because the boot package would no longer hold the
captured state; the `Result` shape concentrates that state and
keeps cmd/tui.go thin without forcing the caller to re-derive
what Build already computed; (e) keep the helpers in `cmd/tui.go`
as one file but split the body into named subroutines
(`buildClientsStage`, `buildAppStage`) — moves the readability
problem inward without solving it, because the precondition
order between subroutines stays implicit and the file still owns
~28 helpers; the package boundary is the real seam, and stopping
short of it would leave the next contributor with the same
review problem.
