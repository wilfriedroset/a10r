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

The body explains the *why*, not the *what*. The diff is the
what.

One commit per logical unit. No WIP history; no fix-up commits in
follow-up PRs.

## Per-commit review process

The project applies a subagent code review to every non-trivial
commit before it lands on `main`. The review is conducted by
spawning a dedicated review agent that classifies findings as
need-work / nice-to-have / nits across maintainability /
testability / scalability / golang-idiomatic. need-work items are
addressed before merging.

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

## Areas where help is welcome

- Mimir-specific actions beyond what v0.1 ships (config viewer,
  ring inspector). The capability flags are already plumbed.
- More themes. The bundled three are intentionally minimal;
  user skins under `<config-dir>/skins/` shadow them by basename.
- Editor integrations beyond vi / vim / nano / notepad. The
  `Resolver.EditorEnv` shape supports anything `$EDITOR` accepts.
- Localisation of the help overlay strings.

[dco]: https://developercertificate.org/
