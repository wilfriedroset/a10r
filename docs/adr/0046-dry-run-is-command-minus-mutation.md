# 0046 — `--dry-run` is the command minus its mutation

A `--dry-run` preview for write commands is part of the agent-CLI
approach HuggingFace published in ["The HF CLI now speaks
agent"](https://huggingface.co/blog/hf-cli-for-agents) (their
`hf upload --dry-run` previews a transfer before it runs); a10r adopts
the affordance and defines its semantics for silence mutations here.

`--dry-run` on a silence write verb runs the verb's full
target-building — the reads, fingerprint resolution, patch merge, and
`skip` computation it already performs — then renders the fully
resolved targets and returns *before* the mutating call (`POST`/`DELETE`
via `op`). The rule is one sentence for all five verbs ("the command,
minus its mutation"), even though the I/O differs per verb:
`create --matcher` contacts no backend because the real verb does not,
while `expire`/`update`/`recreate`/`create --alert` do read because
their real runs do.

## Consequences

- Dry-run is a real *will-it-land* check up to the mutation: it surfaces
  resolved matchers, the merged patch result, id-not-found, and
  already-expired `skip`s. The only failure it cannot predict is one
  that occurs *at* the mutation (Alertmanager rejecting the `POST` for a
  reason invisible to reads).
- **Output** follows ADR 0045. Structured modes emit the resolved
  targets as data (`status: "planned"`, no id for `create` since the id
  is minted at apply, the resolved spec inline); the default lines mode
  prints `would <verb>: …` per target on stdout.
- **Exit code** is the code the real run's pre-mutation phase would
  produce, so a clean dry-run guarantees the real run lands (up to the
  mutation). Write verbs are *not* lenient (unlike read fan-outs):
  `writeExitError` makes any unwritten target — including a reported
  `skip` such as an already-expired id — a non-zero exit, so dry-run
  mirrors that and a skip exits non-zero too. Every target cleanly
  writable exits `0`; a build-phase failure surfaces its ADR-0009 code
  (`3` all-unreachable, `5` not-found). Dry-run is never unconditionally
  `0`, or it would be useless as a pre-commit gate.
- **`--read-only` + `--dry-run`** shows the plan and notes read-only
  rather than refusing: the fail-closed gate exists to prevent a
  mutation, and with no mutation in flight it is moot. The note rides
  stderr in lines mode and a `read_only` field in structured mode.

## Considered and rejected

- **Network-free dry-run** (local validation only, identical behavior
  and zero backend reads for every verb). Attractive for offline preview
  and a uniform I/O story, but it guts the preview for exactly the verbs
  where a preview is most valuable: it cannot resolve a fingerprint to
  matchers, cannot compute a merged patch, and cannot confirm a target
  exists. Offline preview survives anyway where it was ever possible
  (`create --matcher`), so fidelity is the better trade.
- **Strict-refuse under `--read-only`** (`refused: read-only`, no plan).
  A cleaner literal reading of "command minus mutation", but it
  suppresses dry-run's entire value for a cautious operator or agent
  harness that pins `--read-only` on as a standing default.
