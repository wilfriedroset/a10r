# AGENTS.md

a10r is a k9s-shaped terminal UI for Alertmanager and Grafana Mimir:
one YAML config, many tenants, one maintainer. This file routes; the
docs below hold the detail.

## Guardrails

- **Scope.** Feature work starts only on the maintainer's explicit
  go-ahead for that specific feature. Cleanup (file splits, dedup,
  removing over-engineering) goes ahead unasked. When a cleanup opens
  a seam for a feature, ship the cleanup alone and leave the seam;
  "could be a quick win" is not a go-ahead.
- **Contact.** The sole public contact is `a10r@duck.com`; commit
  author email is `wilfriedroset@users.noreply.github.com`. Before a
  change reaches a public branch, grep the tree and confirm no
  personal email address sits in any public file, commit author
  field, or release artefact.
- **Per-commit loop.** Failing test first, `prek -a` clean, review by
  a fresh-context `code-reviewer` subagent, fix every need-work
  finding, re-run both, commit. `Co-Authored-By:` names the agent
  actually used this session. Exemptions and commit format:
  CONTRIBUTING.md.

## Coding standards

The `Project principles`, `Tests first`, and `Comments` sections of
[`CONTRIBUTING.md`](CONTRIBUTING.md) bind too; lint thresholds and the
comment-density budget (ADR 0041) are enforced by `prek -a`.

- **Compose, never fork.** When a third-party library lacks a feature,
  compose around it: a wrapper, or a sibling component rendered
  alongside. A fork is considered only after a wrap-or-replace path is
  ruled out and the missing capability is load-bearing.
- **Refactor for real.** A gocognit / gocyclo / cyclop finding is a
  design signal. Examine the whole function and its collaborators;
  often the right fix changes a collaborator, not just the flagged
  function. Extract a helper only when it removes real duplication or
  names a real concept; slicing code behind a call boundary to push
  the number under the limit hides the complexity rather than fixing
  it.
- **Text.** Code, comments, and docs are emoji-free.
- **Track k9s.** The audience already knows k9s, so an open
  look-and-feel question resolves the k9s way: hint-chip casing (ADR
  0037), and a skin schema that is a drop-in of k9s's so an existing
  k9s skin works unmodified (ADR 0030). Chrome stays slim so the body
  dominates: when space is tight, ornament and chrome give way before
  content (ADR 0036). Skin work:
  [`docs/contributor/skin-authoring.md`](docs/contributor/skin-authoring.md).

## Read when

| Doc | Read when |
|---|---|
| `docs/contributor/sync-checklist.md` | adding a key binding, guardrail action, config field, CLI flag or subcommand, exit code, page, alerts or group detail column, `/` filter mode, or skin |
| `CONTRIBUTING.md` | committing, signing off, or regenerating goldens |
| `docs/contributor/public-copy.md` | writing public text: README, docs, CHANGELOG, issue or PR text, repo metadata |
| `docs/adr/README.md` | writing an ADR or a design note |
| `docs/adr/` | changing behaviour an ADR records (grep the topic) |
| `CONTEXT.md` | meeting or naming a domain term |
| `ARCHITECTURE.md` | locating a package, or tracing a page or backend call |
