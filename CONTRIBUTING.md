# Contributing to a10r

Thanks for considering a contribution. a10r is a small project
with a deliberately narrow surface and high quality bar — please
read this document fully before opening a PR.

## DCO

a10r enforces the [Developer Certificate of Origin][dco]. Every
commit must be signed off:

```sh
git commit -s -m "feat(thing): do the thing"
```

The signoff line (`Signed-off-by: Your Name <you@example>`)
certifies that you wrote the patch yourself or otherwise have the
right to submit it under the project's Apache 2.0 license.

## Pre-commit hooks

```sh
pip install --user prek    # or `pipx install prek`
prek install
```

Every commit triggers:

- `golangci-lint` (strict; ~65 linters incl. cyclop=15, gocognit=20,
  containedctx, depguard, errname, exhaustive).
- `gofumpt` formatting.
- SPDX license header on every Go file.
- Trailing whitespace, EOF, merge conflicts, private keys.

Run the whole suite manually with `prek -a`.

## Tests first

The project follows TDD. Every code commit ships with tests that
cover the happy path *and* meaningful edge cases — invalid
inputs, error paths, concurrency, boundary conditions. Pure-doc /
config-only commits are exempt.

```sh
go test -race ./...
```

Coverage runs via `make cover` (Go's `go test -coverprofile=...`).
There is no minimum-coverage gate; the standard is "every public
behaviour is locked by a test."

## Commit conventions

Conventional Commits: `type(scope): subject`. The accepted types:

- `feat` — new feature.
- `fix` — bug fix.
- `refactor` — no behavioural change.
- `docs` — documentation only.
- `test` — test-only changes.
- `chore` — tooling / build / CI.

When a bug report drives a change that splits across two commits
for bisect-safety (e.g. add dead-code plumbing in commit 1, wire
it up in commit 2), label **both** as `feat:`. The shape is "add
a capability," not "fix a regression in commit 1." Reserve `fix:`
for repairs to previously-shipped behaviour; the motivating bug
report goes in the commit body, not the leading verb.

The body explains the *why*, not the *what*. The diff is the
what.

One commit per logical unit. No WIP history; no fix-up commits in
follow-up PRs.

## Per-commit review process

The project applies a subagent code review to every non-trivial
commit before it lands on `main`. The reviewer classifies findings
as need-work / nice-to-have / nits across maintainability /
testability / scalability / golang-idiomatic / comment-hygiene.
need-work items are addressed before merging.

The exact prompt the reviewer runs is in
[`docs/contributor/review-prompt.md`](docs/contributor/review-prompt.md);
copy it verbatim to reproduce the review with any tool. Claude
Code users spawn it via the `code-reviewer` agent
(`.claude/agents/code-reviewer.md`), which delegates to the same
prompt under a read-only tool restriction.

Pure-doc / config-only commits skip the review but still pass
`prek -a`.

## Project principles

- **No forks.** When a third-party library is missing a feature,
  prefer composing around it (wrappers, sibling components
  rendered alongside) over forking and patching.
- **Constructor injection.** No globals beyond sentinel errors
  and embedded assets (`embed.FS`). Functions take interfaces;
  structs take their dependencies as fields.
- **Table-driven tests.** Where input/output fans out, the test
  is a table. Each row gets a `t.Run(name, ...)` so a failure
  identifies the case.
- **Structured logs.** `log/slog` only. Never bare `log` or
  `fmt.Printf` in production paths.

## Comments

Default to no comment. Code should be self-explanatory through
naming and structure.

Only write a comment when it answers *why*, never *what*. If the
comment restates what the code does, delete it and improve the
code instead.

**Forbidden:**

- Restating the code in English (`// increment counter` above
  `counter++`).
- Section banners (`// --- HELPERS ---`).
- Narrating obvious control flow (`// loop through users`).
- TODOs without a ticket reference or owner.
- Docstrings that just list parameters already visible in the
  signature.
- Preamble explaining what the code is about to do.
- Marking what changed in this edit (`// updated to handle null`)
  — that's what the diff is for.

**Acceptable, when truly needed:**

- Non-obvious *why*: business rule, spec reference, workaround for
  an external bug, performance trade-off, intentional deviation
  from the obvious approach.
- Warnings about non-local consequences ("called by X under Y
  condition").
- Links to issues, ADRs, RFCs, or external docs.

One line is the target. If the explanation needs more lines than
the code, refactor instead. Re-read nearby comments when editing;
delete or update any that no longer match.

## Areas where help is welcome

- Mimir-specific actions beyond what v0.1 ships (config viewer,
  ring inspector). The capability flags are already plumbed.
- More themes. The bundled twelve span two families (catppuccin and
  ovhcloud); user skins under `<config-dir>/skins/` shadow them by basename.
- Editor integrations beyond vi / vim / nano / notepad. The
  `Resolver.EditorEnv` shape supports anything `$EDITOR` accepts.
- Localisation of the help overlay strings.

[dco]: https://developercertificate.org/
