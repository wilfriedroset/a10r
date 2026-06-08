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
| `5`  | Not found. A `get` / `update` / `expire` / `recreate` command named a resource (alert fingerprint, silence id) that no backend in scope confirmed, while at least one backend answered. Distinct from `3` so a script can tell "the resource is gone" (e.g. recreate it) from "I could not reach a backend to check" (retry later). |
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

## Structured error envelope

When a command fails before producing a result and a structured format
is in effect (`--output=json|yaml`, `A10R_OUTPUT`, or a detected agent),
the error is also written to stderr as `{"error": "...", "code": N}`
where `code` equals the exit code above — so a wrapper can parse the
reason without the exit code and the message ever disagreeing. stdout
stays empty on such a failure. Under a human format the failure is a
plain stderr message; either way the exit code is the contract. See
[output-formats.md](output-formats.md#errors).

## Dry-run

`--dry-run` exits with the code the real run's pre-mutation phase would
produce: `0` when every target is cleanly writable, and the same
non-zero code the real run would give when a target cannot land — a
not-found or already-expired id, or an all-unreachable scope. (Writes
are not lenient, so a reported skip is non-zero, unlike the read
fan-out's partial rule above.) A clean dry-run is therefore a reliable
pre-commit gate.

## Stability

The numeric values are a stable contract from v0.1.0 onward. Future
codes will be appended (never inserted between existing values) so
existing wrappers do not break.
