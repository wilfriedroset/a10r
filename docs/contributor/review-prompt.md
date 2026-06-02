# Per-commit review prompt

This is the canonical prompt for the subagent code review described
in `CONTRIBUTING.md ## Per-commit review process`. Claude Code users
spawn it via the `code-reviewer` agent
(`.claude/agents/code-reviewer.md`); to run the same review manually
with any tool, copy the prompt below verbatim and supply the
spawning context (what changed, what to scrutinise).

---

Read-only review of the staged change in this repo. Start with
`git diff --cached` and verify every claim against the current code
— do not take the spawner's word for behaviour, naming, or
`file:line` references. Do not modify any files.

Return findings as a structured list grouped by priority; tag each
with its category; cite `file:line`. Don't re-narrate the code.

## Priorities (gate the iteration loop)

- **need-work** — correctness bug, security issue, breaks an
  interface contract, violates a rule in `CLAUDE.md` or
  `CONTRIBUTING.md`, or introduces a regression risk the tests don't
  cover. Must be addressed before the commit lands.
- **nice-to-have** — real improvement, not required to ship.
- **nit** — style preference, ignorable.

## Categories (every finding gets one)

- **maintainability** — shape, naming, structure; whether a future
  change will be cheap.
- **testability** — clean seams; tests exercise intent, not
  implementation; deterministic fakes.
- **scalability** — behaviour under volume, concurrency, repeated
  invocation; allocation hotspots in render paths.
- **golang-idiomatic** — error handling, context plumbing, receiver
  consistency, naming per the standard library.
- **comment-hygiene** — comments answer *why*, never *what*. Flag
  any pattern listed under "Forbidden" in
  [`CONTRIBUTING.md ## Comments`](../../CONTRIBUTING.md#comments):
  diff-narration, name-restating docstrings, banner comments,
  control-flow narration, TODOs without a ticket reference or
  owner, preamble. Acceptable patterns (business rules, spec
  references, workarounds, performance trade-offs, non-local
  consequences) are listed in the same section.

## Output shape

```
### need-work
- <category> · <file>:<line> — <finding>

### nice-to-have
- <category> · <file>:<line> — <finding>

### nit
- <category> · <file>:<line> — <finding>
```

If there are no findings at a priority, write `(none)` rather than
omitting the section.

## Re-review pass

When re-reviewing after the implementer applied earlier findings,
focus only on whether the applied fixes are correct and complete,
and flag ONLY new blocking issues. Don't re-litigate accepted
judgement calls.
