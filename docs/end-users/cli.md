# Headless CLI

Run with no subcommand, a10r launches the TUI. With a subcommand it
runs headless — for scripts, CI gates, and on-call one-liners. Every
subcommand also self-documents via `--help`.

The read verbs (`list`, `get`) and the silence write verbs
(`create`, `update`, `expire`, `recreate`) give the CLI the same
alert-triage and silence-lifecycle reach as the TUI.

## Scope: `--tenant`

`--tenant` selects which configured backends a headless command acts
on — the same `<name>` / `all` / `a,b` syntax as the TUI's `:tenant`.
It defaults to every backend. A `--tenant` value that names no
configured backend is an error, not a silent empty result.

```
a10r alerts list --tenant prod          # only the prod backend
a10r silences list --tenant prod,staging
```

## Reading

```
a10r alerts list   [--severity <s>] [--state <active|suppressed|unprocessed>] [--fail]
a10r silences list [--state <active|pending|expired>] [--matcher <m>] [--fail]
a10r alerts get    <fingerprint>
a10r silences get  <id>
```

`get` shows one record in full (the TUI detail view): `alerts get`
takes a fingerprint (the alert instance's stable identity — to act on
every instance of an alertname, use a `--matcher` instead), `silences
get` takes a silence id. Both search every in-scope backend and tag
each match with its tenant; an identity that lives nowhere exits `5`
(not found), distinct from `3` (a backend was unreachable).

The identity to pass comes straight from the list: `alerts list` has a
FINGERPRINT column and `silences list` an ID column, so the usual flow
is `a10r alerts list` → copy a fingerprint → `a10r alerts get <fp>`
(or `a10r alerts list -o json | jq -r '.[].fingerprint'`).

## Writing silences

```
a10r silences create   (--matcher <m>... | --alert <fingerprint>...) \
                        --comment <text> [--starts <when>] [--ends <when>] [--created-by <who>]
a10r silences update    <id> [--matcher <m>...] [--starts <when>] [--ends <when>] [--comment <text>] [--created-by <who>]
a10r silences expire     <id> [<id>...]
a10r silences recreate  <id> --ends <when> [--comment <text>] [--created-by <who>]
```

- **create** — `--matcher` (repeatable, Prometheus syntax) authors a
  silence directly; `--alert <fingerprint>` derives the matchers from
  an alert instance's labels (the TUI's silence-one). The two are
  mutually exclusive. `--alert` is repeatable, and each fingerprint
  becomes its own silence — the headless analogue of marking several
  alerts and silencing them at once in the TUI. `--ends` accepts a
  duration (`2h`, `7d2h`, added to the start) or an RFC3339 timestamp,
  and defaults to `2h`; `--starts` defaults to now; `--comment` is
  required; `--created-by` defaults to `$USER` then `a10r`.
- **update** — patches in place: only the flags you pass change, the
  rest of the silence is kept, so `--ends 4h` alone extends a silence.
  Repeatable `--matcher` replaces the whole matcher set. Only active
  or pending silences are mutable; an expired one points you at
  `recreate`.
- **expire** — expires each id. No confirmation prompt: the explicit
  id is the confirmation. Lenient per id (a missing or already-expired
  id is reported but the others still expire), and the command exits
  non-zero if any id failed.
- **recreate** — derives a new silence from an existing one, copying
  its matchers and comment but restating the window (start resets to
  now, author to you). `--ends` is required — recreate never reuses
  the source's window. To change the matchers, use `create`.

### Targeting and the fail-closed rule

`--alert` and the silence id verbs derive their target tenants: the
silence lands in / is acted on in each in-scope backend where the
fingerprint or id resolves. `create --matcher` cannot derive a tenant,
so with more than one backend in scope it requires an explicit
`--tenant` (`--tenant all` to fan out deliberately).

Writes **fail closed**: a10r resolves the full target set first, and if
the session is read-only (`--read-only`, `A10R_READ_ONLY`, or
`defaults.read_only`) or any target backend is individually read-only,
the command writes nothing and tells you which backend blocked it.
Narrow `--tenant` to the writable set to proceed.

## Output and exit codes

See [output-formats.md](output-formats.md) for the `--output` contract
(table/json/yaml for reads, `tenant<TAB>id` lines for writes) and
[exit-codes.md](exit-codes.md) for the exit-code table CI wrappers
branch on.
