# Output formats

Read-only commands (`a10r alerts list`, `silences list`, `doctor`,
etc.) accept `--output=<format>` to switch the rendering between
human-friendly tables and machine-friendly structured formats.

| Value   | When to use                                        |
| ------- | -------------------------------------------------- |
| `table` | Default on a TTY. Aligned columns, headers in caps. |
| `json`  | Default on a pipe. Indented JSON, jq-friendly.      |
| `yaml`  | YAML with 2-space indent for diffing / config snippets. |

When `--output` is unset, a10r resolves the format in precedence
order — the `--output` flag, then the `A10R_OUTPUT` environment
variable, then AI-agent detection, then the TTY-derived default:

1. an explicit `--output` flag always wins;
2. else `A10R_OUTPUT=<format>` (best-effort: a value a given command
   cannot render is ignored rather than erroring, so an ambient default
   never breaks a command a bare invocation would have run);
3. else `json` when a10r detects it is running under an AI coding agent
   (see below);
4. else `table` if stdout is a terminal, `json` if it is a pipe — so
   `a10r alerts list | jq` "just works" without a flag.

## Agent mode

a10r detects common AI coding agents from environment markers they set
(`CLAUDECODE`, `CURSOR_TRACE_ID`, `CODEX_SANDBOX`, the universal
`AI_AGENT`/`AGENT`, and others) and defaults to `json` everywhere —
including the write verbs, which otherwise default to the
`tenant<TAB>id` lines mode. This means an agent gets structured,
parseable output by default with no per-call `--output=json`, even when
its stdout is a pseudo-terminal that the TTY probe would read as human.

Detection is a default, not a lock: an explicit `--output` (or
`A10R_OUTPUT`) still wins, so you can force a table inside an agent.
Set `A10R_OUTPUT=json` in a harness a10r does not auto-detect to get
the same behavior.

## Detail commands (`get`)

`a10r alerts get <fingerprint>` and `a10r silences get <id>` render
one record, not a row grid, so their defaults differ from the list
commands: `yaml` on a TTY (a record reads and diffs better as a
document than as a one-row table) and `json` in a pipe — the silence
spec fields (matchers, starts/ends, comment, createdBy) line up with
the TUI's editor buffer, though the rendered record also carries
read-only context (tenant, state) that is not part of that buffer.
`--output=table` is rejected — pass `json` or
`yaml`. When the same identity resolves in several tenants (a
mirrored alert, a mirrored silence id) the output becomes a sequence
of all matches; a single match renders as one document.

## Write commands (`create` / `update` / `expire` / `recreate`)

The silence write verbs emit the machine-usable result on stdout and
keep human narration / errors on stderr, so a pipeline stays clean:

- Default: one `tenant<TAB>id` line per silence written, on stdout.
  `a10r silences create --tenant prod --matcher 'severity="critical"'
  --comment maint | cut -f2` yields just the new id.
- `--output=json` / `--output=yaml`: the full result array, one
  `{tenant, id, status, error}` per target (so a partial bulk failure
  reports what landed and what did not). `--output=table` is rejected.
- Any failed write makes the command exit non-zero — unlike the read
  fan-out, a write is never silently lenient. See
  [exit-codes.md](exit-codes.md).

## Next-step hints

After a successful write, a10r prints an undo/verify hint to **stderr**
(in every output mode, so stdout stays pure data) giving the exact
reverse command for what just landed:

```
$ a10r silences create --tenant prod --matcher 'severity="critical"' --comment maint
prod    a1b2c3                                            # stdout: the result
expire with: a10r silences expire a1b2c3                 # stderr: the undo
```

- `create` / `recreate` → the `expire` that removes the new id(s);
  the variadic `expire` collapses a fan-out into one line.
- `expire` → the `recreate` that restores it (only when a single
  silence was expired; a bulk expire suppresses the hint).
- `update` → the `get` that shows the merged result.

Hints are guidance, never data: a `--output=json` consumer reads the
result from stdout and can ignore stderr entirely.

## Dry-run plans

The write verbs accept `--dry-run`: a10r resolves everything the real
run would (matchers, the merged patch, whether each id exists) and
prints the **plan** without writing anything. Output mirrors the format
selection — `json`/`yaml` emit an array of plan records on stdout,
the default lines mode prints a `would <verb> …` summary:

```
$ a10r silences expire sil-1 --dry-run -o json
[
  { "tenant": "prod", "action": "expire", "id": "sil-1" }
]
```

A create/recreate plan carries the resolved `matchers`, `starts_at`,
`ends_at`, `comment`, and `created_by` (but no `id` — that is minted at
apply); a skipped target carries a `skip` reason; a target in a
read-only backend carries `read_only: true` (dry-run plans even under
read-only — it never writes, so it is never refused). The dry-run exit
code mirrors the real run's pre-mutation phase, so a clean dry-run is a
true pre-commit gate (see [exit-codes.md](exit-codes.md)).

## Errors

When a command fails *before producing a result* (bad config, every
backend unreachable, a missing get target, a malformed invocation) and
a structured format is in effect (`--output=json|yaml`, `A10R_OUTPUT`,
or a detected agent), a10r renders the error as a structured envelope on
**stderr** — stdout stays empty:

```
{ "error": "validate config: backend \"prod\": at most one of basic_auth, authorization, bearer_token may be configured", "code": 2 }
```

`code` mirrors the exit code (see [exit-codes.md](exit-codes.md)). Under
a human format the same failure is a plain message. The exit code is the
contract; the envelope is parseable detail. Per-target write failures
are *not* top-level errors — they ride inside the write result array on
stdout — and `--fail`'s exit `10` is a signal, not a failure, so neither
is enveloped.

## Pager

When `--output=table` (the default on a TTY) renders to a
terminal, the output is piped through `$PAGER` (falling back to
`less -FRX` when unset). The `-FRX` flags mean less quits if the
output fits on one screen, passes ANSI colour through, and does
not switch to the alternate screen — so the rendered output
stays visible after the pager exits, k9s-style.

Pass `--no-pager` to disable the wrapper. The pager is also
disabled automatically for `--output=json|yaml` (you almost
always pipe those into a downstream tool) and when stdout is not
a terminal.

## Stability

> **Pre-v1 disclaimer:** the structural shape of `--output=json`
> and `--output=yaml` is unstable. Field names, nesting, and
> defaults MAY change between v0.x releases without a deprecation
> window. Pin a specific a10r version (`go install
> github.com/wilfriedroset/a10r@v0.1.0`) in CI/CD scripts that
> parse the output until v1.0 freezes the schema.

The `table` format is for humans and is allowed to change freely
(column re-ordering, label rendering, severity colouring) at any
release.

## Notes for scripting

- JSON output ends with a newline so `tail -n 1` and similar
  tools see the last record cleanly.
- HTML escaping is disabled: URLs in alert annotations / labels
  retain literal `&`, `<`, `>` so jq pipelines see human-readable
  values rather than `&`-style escapes.
- YAML output uses 2-space indent (matching the file-side `a10r.yaml`
  convention) and yaml.v3's default key ordering, so it reads and
  diffs cleanly. It is a view of the resource, not the config schema —
  it is not meant to be pasted into `a10r.yaml`.
