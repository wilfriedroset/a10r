## 0033 — `boot.Build` Stage-N narration replaced by named functions

`internal/tui/boot.Build` is the TUI's startup entry point. Pre-
refactor the function spanned ~180 LOC across eleven blocks each
prefixed with a `// Stage N — <name>. <rationale>.` multi-line
comment that narrated what the next 4–8 lines would do plus the
preconditions for the next stage. A comment audit flagged the shape
directly: the prose was doing the work that function names should be
doing,
and a reader had to read both the comment and the code to follow
startup.

Each stage now lives in its own named function. `Build` reads as a
flow of operations — `loadConfigForTUI`, `resolveEffectiveConfig`,
`initLogger`, `logTransportSurprises`, `buildClients`,
`buildTenantRows`, `resolveConfigDirAndStyles`, `buildDispatcher`,
`buildPageEnv`, `buildApp`, `applyUserKeyOverrides` — instead of
narrating its own structure. The function names carry the
precondition rationale the Stage-N preambles used to encode; each
extracted helper grows the precondition into its own doc comment
where one is non-obvious (e.g. `initLogger` opens with "must run
before any subsystem can emit"; `buildDispatcher` documents the
chord registration timing). Stages that were already one-line
function calls (`logTransportSurprises`, `applyUserKeyOverrides`,
`buildTenantRows`) stay as they were; the win was specifically in
the inline blocks.

The Stage 9 → Stage 10 dependency on the not-yet-constructed `*app.App`
(via the `timeFormat` closure) is preserved by passing a
`*app.App` pointer into `buildPageEnv` that gets assigned to in
`buildApp` — closures read it at invocation time, so the order of
`buildPageEnv` then `buildApp` keeps the live-app-global value the
TimeFormat callback needs.

Considered and rejected: (a) leave the inline blocks and just trim
the Stage-N comments — silences the audit complaint but discards the
real structure (each block IS a logical step) and leaves Build at
180 LOC with no signposts for the reader. (b) Adopt a uniform
`(updatedDeps, error)` shape per stage as the audit suggested
literally — would force every stage into a single struct that
accumulates fields, hiding which stage actually produces each
output. Each helper's natural signature (taking only what it needs,
returning only what subsequent stages consume) reads better than a
uniform mutator shape. (c) Push the whole bootstrap into a
`bootstrapper` value with methods — overcorrect; `Build` is called
once per process, no second caller is in scope, no fan-out exists to
amortise the type.
