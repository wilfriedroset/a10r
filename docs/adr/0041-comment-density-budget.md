# 0041 — Comment standard, enforced by a per-package density budget

The codebase's comment rule has always been: **default to no comment;
write one only when it answers WHY, never WHAT; one line is the target;
if the explanation needs more lines than the code, refactor instead.**
That rule lived in CLAUDE.md but nothing enforced it, and the code
drifted — an AI-slop audit found ~32% comment lines across non-test
code, with individual files at 60-80%. The comments were rarely the
forbidden kind (almost no code-restatement, no banners); the drift was
legitimate WHY-comments running 3-6x too long plus a thin layer of
genuine violations. A one-off scrub would drift back. This ADR records
the standard as the enforced convention and the guard that keeps it.

The per-comment decision tree (the standard, applied by judgment — there
is no target density to chase):

- **DELETE** — restates the code, docstrings a trivial accessor, narrates
  obvious control flow, or repeats boilerplate stated elsewhere.
- **COMPRESS to one line** — a real WHY currently spread over several
  lines; keep the load-bearing clause, cut the prose. If it cannot fit
  one line, refactor (named helper/const) so it can.
- **KEEP verbatim** — load-bearing WHY already tight: security rationale,
  external-runtime workaround, spec/ADR/RFC reference, non-local-
  consequence warning, concurrency invariant.

The guard is `make lint-comments` (`scripts/lint-comments.sh`), wired
into pre-commit/CI. It fails when any non-test package exceeds a comment-
line budget (default 55%), measured **per package** so a tight package
doc on a short file does not trip it and **per package, not per file** so
one bloated file cannot hide behind its siblings — and skips packages
under 40 lines, where the ratio is noise.

The budget is a **regression ceiling, not a quality target**. It sits
deliberately above the density the trim left (worst substantial package
~49%) so it catches a package ballooning back toward the 60-80% AI-slop
range without nitpicking well-documented code. The standard — the
decision tree above — is the real bar; the number just stops the drift.

Considered and rejected: (a) a hard low target (e.g. 20%) enforced
globally — punishes legitimately doc-heavy small files and turns the
guard into a goal to game, the opposite of "write the WHY you need";
(b) per-file enforcement — trips on short files whose package doc plus
two exported-decl comments are entirely appropriate; (c) ADR only, no
CI gate — the drift already happened once under a documented-but-
unenforced rule; (d) a linter plugin (e.g. a custom golangci analyzer) —
more machinery than a 40-line shell script for a blunt density check.
