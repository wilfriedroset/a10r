# a10r

## Context

I want to create a modern, fast and intuitive TUI written in golang for alertmanager with a look-and-feel similar to [k9s](https://github.com/derailed/k9s).
I want the codebase built on libs that are both powerful and simple to use and extend, easy to maintain and test, vibrant community, good documentation.

## Target

The TUI is aimed for tech savy such as developer, SRE, devops. It should support [vanilla alertmanager](https://github.com/prometheus/alertmanager) and [grafana's mimir](https://github.com/grafana/mimir).
It should be configurable via a yaml file and support multiple "tenants" where the user can interact with a given tenant, multiple ones or all of them at once.
The TUI must support vim motion and friction less short-hand (? to display the help)

## Principles

- **No forks.** This is a pet project; maintaining a fork is overhead we don't want. When a third-party library is missing a feature, prefer composing around it (wrappers, sibling components rendered alongside) over forking and patching. Only consider a fork after a wrap-or-replace path has been ruled out and the missing capability is genuinely load-bearing.
- **Clean codebase from day 1.** Favour patterns that keep the code testable and reviewable: TDD (tests first), dependency injection (functions take interfaces, structs take their deps as fields, no globals beyond sentinel errors and embedded assets), table-driven tests for anything with input/output fan-out, small focused units. Every code commit ships with tests for the happy path and the meaningful edge cases, must pass `prek -a`, and is reviewed before landing. One commit per logical unit — no WIP history, no fix-ups in follow-ups.

## Resources

To avoid fetching over internet, clone repos under /home/debian/workspace/github.com, for example /home/debian/workspace/github.com/derailed/k9s
