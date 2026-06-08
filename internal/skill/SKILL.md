---
name: a10r
description: "Manage Prometheus Alertmanager and Grafana Mimir from the terminal with the `a10r` CLI: inspect alerts, groups, and receivers; create, update, expire, and recreate silences; scope by tenant; run backend health checks. Use when the user wants to view firing alerts, silence noise, check receiver routing, or check that the configured Alertmanager/Mimir backends are reachable — even if a10r isn't named — and the `a10r` binary is on PATH."
---

# a10r

`a10r` is a CLI/TUI for Prometheus Alertmanager and Grafana Mimir. Run with a
subcommand it is headless — built for scripts, CI, and AI agents. Use it to
inspect alerts, alert groups, and receivers, and to manage silences.

## Before you start

1. Confirm the binary exists: `command -v a10r`. If it is absent, this skill
   does not apply — stop and tell the user a10r is not installed.
2. a10r reads a single `a10r.yaml` from an XDG-resolved per-OS directory
   (`$XDG_CONFIG_HOME/a10r/a10r.yaml`; `-c <path>` or `--config-dir <dir>` override
   it). It lists the backends (tenants). Confirm config and connectivity before acting:
   - `a10r info` — the *resolved* config path (use this to find the actual file)
     and the backends it found.
   - `a10r validate` — config parses and every backend is usable.
   - `a10r doctor` — live reachability/auth/version-floor checks per backend.

`validate` and `info` are text-only diagnostics with no `--output` flag — branch
on their exit code (see the table below), do not parse their stdout. Everything
else about JSON output applies to the data and verb commands.

## Output contract — read this before parsing anything

You do **not** need `--output=json`. For the data and verb commands (`alerts`,
`silences`, `groups`, `receivers`, `doctor`) a10r detects coding agents from env
markers (`CLAUDECODE`, `CURSOR_TRACE_ID`, `CODEX_SANDBOX`, `AI_AGENT`, …) and
defaults to **JSON on stdout**, including the write verbs. So:

- **stdout is pure machine data** — parse it directly.
- **stderr is narration** — next-step hints, warnings, never data. Ignore it
  when consuming stdout, but it carries the next-step hint after a write (below).
- **Failures before a result** emit `{"error": "...", "code": N}` on stderr with
  stdout empty. `code` equals the exit code, so message and status never disagree.
- Override the auto-format per call with `--output=table|json|yaml` if you must.

### Exit codes (the contract — branch on these, not on stderr text)

| Code | Meaning |
| ---- | ------- |
| `0`  | Success (or `--fail` set and nothing matched). |
| `1`  | Generic runtime error. |
| `2`  | Config invalid — fix `a10r.yaml`. |
| `3`  | Every backend in scope unreachable — fix connectivity (retry later). |
| `4`  | Every backend in scope rejected credentials (401/403) — fix auth. |
| `5`  | Resource not found (a `get`/`update`/`expire`/`recreate` target) while a backend answered — it is gone, not unreachable. |
| `10` | `--fail` predicate matched: a list command found matching rows. |

These values are a stable, append-only contract (`docs/end-users/exit-codes.md`
is authoritative; future codes are appended, never reassigned) — so a newer a10r
may add a code this table omits, but never changes one.

Multi-tenant partial failure (some backends ok, some not) exits `0` with stderr
warnings — parse `a10r doctor -o json` per-backend rows if you must fail on it.

## Safety — never mutate blindly

The silence write verbs (`create`, `update`, `expire`, `recreate`) change state.
Before any of them:

- **`--dry-run`** resolves matchers, the merged patch, and whether each target id
  exists, then prints the plan **without writing**. Its exit code mirrors the real
  run's pre-mutation phase, so a clean dry-run (exit `0`) is a reliable pre-commit
  gate. Always dry-run a write you are not certain about, and show the user the plan.
- **`--read-only`** hard-disables every write verb for the session. Dry-run still
  plans under it (it never writes), marking targets `read_only: true`.
- After a successful write, a **next-step hint is on stderr**: `expire with: …`
  after create/recreate (the undo), `recreate with: …` after a *single* expire,
  `verify with: …` after update. Capture it to offer the user an undo or check.
  It is suppressed when the follow-up would need more than one command (a
  multi-id expire prints no hint), so do not rely on it after a fan-out write.

## Commands

`a10r --help` lists everything; `a10r <command> --help` has flags and worked
examples for each. The verbs that carry judgment:

- `a10r alerts list [--severity S] [--state active|suppressed|unprocessed] [--fail]`
  — firing alerts. `--fail` exits `10` on any match (CI gate). `get <fingerprint>`
  shows one alert.
- `a10r silences create --comment C (--matcher 'k="v"' … | --alert <fingerprint>) [--ends 2h] [--starts …]`
  — `--matcher` and `--alert` are mutually exclusive; `--ends` takes a duration
  (`2h`, `7d2h`) or an RFC3339 timestamp (`2026-06-09T15:00:00Z`). `--comment`
  is required.
- `a10r silences expire <id> …` — variadic; the undo hint collapses a fan-out.
- `a10r silences update <id> …` — patch an active/pending silence in place.
- `a10r silences recreate <id>` — new silence from an existing one, window restated.
- `a10r silences list` / `silences get <id>` — inspect.
- `a10r groups list`, `a10r receivers list` — alert grouping and receiver routing.
- `a10r doctor [--only reachability,auth]`, `a10r validate`, `a10r info`
  (`a10r doctor --help` lists every check name).

## Tenants

`--tenant` scopes every command: `--tenant prod` (one), `--tenant a,b` (several),
`--tenant all` (fan out across all configured backends). Read commands fan out and
merge; a write without `--tenant` targets whatever the config resolves as default —
prefer naming the tenant explicitly on a write so it lands where you intend.

## Worked examples

Find critical alerts, then silence one for two hours after previewing the plan:

```
a10r alerts list --severity critical
a10r silences create --tenant prod --matcher 'alertname="HighCPU"' \
    --comment "investigating" --ends 2h --dry-run    # inspect the plan first
a10r silences create --tenant prod --matcher 'alertname="HighCPU"' \
    --comment "investigating" --ends 2h              # apply; undo hint on stderr
```

Silence a specific alert instance by fingerprint, then expire it:

```
fp=$(a10r alerts list --severity critical | jq -r '.[0].fingerprint')
id=$(a10r silences create --alert "$fp" --comment "deploy" --ends 4h | jq -r '.[0].id')
a10r silences expire "$id"
```

CI gate — page when any critical alert is firing:

```
a10r alerts list --severity critical --fail || notify-oncall   # exit 10 == matched
```
