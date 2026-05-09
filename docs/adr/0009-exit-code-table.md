# 0009 — Exit code table for CLI commands

The CLI returns `0` (success), `1` (runtime error), `2` (config
invalid — parse or validate fail), `3` (all configured backends
in scope unreachable — network/DNS/timeout), `4` (all configured
backends in scope auth-failed — 401/403), and `10` (`--fail`
predicate matched on `alerts list` / `silences list`); partial
failure across a multi-tenant scope exits `0` with stderr
warnings, so codes `3` and `4` fire only when *every* tenant
failed the same way. The expanded table over POSIX-minimal
`0/1/2` lets CI wrappers branch on remediation type without
parsing stderr — fix-credentials vs fix-network vs fix-config are
distinct user actions. The lenient partial-failure rule keeps
single-tenant blips from breaking otherwise-green pipelines, at
the cost of requiring on-call wrappers to grep stderr when they
care about partial degradation.
