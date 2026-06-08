# Output formats

Read-only commands (`a10r alerts list`, `silences list`, `doctor`,
etc.) accept `--output=<format>` to switch the rendering between
human-friendly tables and machine-friendly structured formats.

| Value   | When to use                                        |
| ------- | -------------------------------------------------- |
| `table` | Default on a TTY. Aligned columns, headers in caps. |
| `json`  | Default on a pipe. Indented JSON, jq-friendly.      |
| `yaml`  | YAML with 2-space indent for diffing / config snippets. |

When `--output` is unset, a10r picks `table` if stdout is a
terminal and `json` if it is a pipe — so `a10r alerts list | jq`
"just works" without an explicit flag.

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
