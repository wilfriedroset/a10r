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

## Stability

> **Pre-v1 disclaimer:** the structural shape of `--output=json`
> and `--output=yaml` is unstable. Field names, nesting, and
> defaults MAY change between v0.x releases without a deprecation
> window. Pin a specific a10r version (`go install
> github.com/wilfriedroset/a10r@v0.0.1`) in CI/CD scripts that
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
- YAML output uses 2-space indent (set explicitly to match the
  file-side `a10r.yaml` convention) and yaml.v3's default key
  ordering; a `--output=yaml` snippet can usually be pasted into
  `a10r.yaml` without reformatting.
