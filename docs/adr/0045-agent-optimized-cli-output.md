# 0045 — Agent-optimized CLI output: stream contract and mode selection

This redesign adapts the agent-CLI approach HuggingFace published in
["The HF CLI now speaks
agent"](https://huggingface.co/blog/hf-cli-for-agents) and implemented
in their `hf` CLI — the stdout/stderr split, hints-on-stderr-in-all-
modes, structured output, and presence-based agent detection are theirs;
a10r diverges where noted below (a `json` default rather than a dense
TSV `agent` format, and no generated skill). The decisions are recorded
here in a10r's own terms because the trade-offs still had to be made
against a10r's existing exit-code (ADR 0009) and precedence (ADR 0027)
contracts.

In every output mode, **stdout carries only data and stderr carries
hints, warnings, and errors** (plain text, no ANSI when off-TTY); the
structured modes (`json`/`yaml`) keep stdout pure so `| jq` and
`> out.json` never see narration, while stderr stays useful in all
modes. The output mode is selected by the precedence **`--output` flag
→ `A10R_OUTPUT` env → agent detection → TTY-derived default**, where
agent detection (presence of a known coding-agent env marker) selects
`json`; this extends the chain in ADR 0027 (see the cross-reference
there).

## Consequences

- **Hints** are next-step suggestions emitted on stderr in *every* mode.
  They are *undo-primary*: `create`/`recreate` suggest the `expire` that
  reverses them (the new id pre-filled), `expire` suggests `recreate`,
  and `update` is the exception — it suggests `get` to verify the merged
  result. A hint is emitted only when the follow-up *collapses to a
  single command* (the variadic `expire` absorbs a `create` fan-out into
  one line; a multi-id `expire` suppresses its per-id `recreate` hints).
- **Top-level failures** render as `{error, code}` on stderr in the
  selected structured format, with `code` mirroring the ADR-0009 exit
  code and stdout left empty. This is uniform across every non-zero exit
  including cobra arg/usage errors (best-effort off the bound `--output`
  value). Per-target write failures are *not* top-level errors — they
  stay in the stdout data array (`[]writeResult`); `--fail` matched
  (exit 10) is not an error and is not enveloped.
- **`A10R_OUTPUT`** is best-effort: a value invalid for a command (e.g.
  `table` on `get`) falls back to that command's normal default rather
  than erroring, because an ambient env default must not break a command
  a bare invocation would have run. An explicit `--output=table` on
  `get` still errors — it was asked for by name. This deliberately
  differs from 0027's loud-over-silent rule, which is reserved for the
  security-relevant read-only gate.
- The env marker set mirrors the HuggingFace CLI's: the universal
  `AI_AGENT`/`AGENT`, plus per-tool markers (`CLAUDECODE`/`CLAUDE_CODE`,
  `CODEX_SANDBOX`/`CODEX_CI`/`CODEX_THREAD_ID`,
  `CURSOR_TRACE_ID`/`CURSOR_AGENT`, and the rest), presence-only, in one
  documented table that is cheap to extend.

## Considered and rejected

- **Structured modes carry no hints (hints in the default lines mode
  only).** Held briefly, then reversed: a hint on stderr never pollutes
  the parsed stdout, so suppressing it for agents gains nothing and
  costs them a useful next-command line — the same conclusion the
  HuggingFace CLI reached. The purity guarantee is scoped to stdout, not
  to "no narration anywhere".
- **Brand-sniffing is too brittle; use only TTY detection (or only an
  explicit `A10R_OUTPUT`).** Dropped once it was clear that reading
  `A10R_OUTPUT` and reading `CLAUDECODE` are the same mechanism (a
  getenv), and that an explicit env has zero out-of-box value because
  nobody sets it on day one — whereas agents already export their own
  markers. TTY detection remains the baseline (it correctly classifies
  the piped stdout of today's agents); detection is the cheap layer that
  also closes the PTY-allocating-agent hole, and `A10R_OUTPUT` is kept
  as the explicit override for harnesses we do not auto-detect.
- **A dense TSV `agent` output format** (as the HuggingFace CLI ships).
  a10r's `json`-on-detection already serves agents well; a third format
  is surface area for little gain.
- **The error envelope on stdout** so a `-o json | jq` pipeline always
  has a document to parse. Rejected: it would make stdout sometimes data
  and sometimes an error on the same stream, breaking the stdout=data
  rule and forcing every consumer to discriminate before every parse.
  The exit code is the contract; the stderr envelope is parseable
  detail.
