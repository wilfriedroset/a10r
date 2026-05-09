# Exit codes

a10r returns one of the following exit codes from any subcommand.
CI/CD wrappers branch on these values to distinguish remediation
types without parsing stderr — for example `a10r alerts list
--severity=critical --fail || page-oncall`.

| Code | Meaning |
| ---- | ------- |
| `0`  | Success. The command produced its intended output (or matched no rows when `--fail` was set on a list-style command). |
| `1`  | Generic runtime error. Falls through when no more-specific code applies. |
| `2`  | Config invalid. `a10r.yaml` could not be parsed or failed schema validation. Operator action: fix the config file. |
| `3`  | Backend unreachable. Every configured backend in the active scope failed to respond at the network layer (DNS, timeout, connection refused). Operator action: fix network connectivity. |
| `4`  | Backend authentication failed. Every configured backend in the active scope rejected the credentials with 401/403. Operator action: fix credentials. |
| `10` | `--fail` predicate matched. A list-style command (`alerts list`, `silences list`) was invoked with `--fail` and at least one row matched the filter. |

## Partial failure (multi-tenant)

Codes `3` (unreachable) and `4` (auth-failed) fire only when *every*
backend in scope failed in the same way. Partial degradation —
some tenants ok, some unreachable — exits `0` with stderr
warnings; the operator decides whether the partial picture warrants
intervention. This is intentional: a single-tenant blip should not
break an otherwise-green pipeline.

If your CI pipeline needs to fail on partial degradation as well,
parse the JSON output of `a10r doctor --output=json` and branch on
the per-backend severity rows directly.

## Stability

The numeric values are a stable contract from v0.0.1 onward. Future
codes will be appended (never inserted between existing values) so
existing wrappers do not break.
