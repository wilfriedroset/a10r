# 0021 — Prom-style matcher parsing lives in `internal/matcher`

The `name<op>value` Prom-style matcher parser shipped in two
byte-identical copies: `cmd/silences.go` carried a `promMatcher`
value type plus `parsePromMatcher` for the `--matcher` flag, and
`internal/tui/form/silence/form.go` carried `parseOneMatcher` /
`parseMatchers` for the silence form's textarea. Both walked the
same four-operator table (`=`, `!=`, `=~`, `!~`), both leaned on
the same two-char-wins-on-tie invariant (load-bearing for round-
trips: `foo=a!=b` must split on the leftmost `=`, and `foo=~bar`
must parse as a regex match rather than a literal-equal of
`~bar`), and the cmd-side comment block from `cmd/silences.go:412`
explicitly named the extraction as deferred follow-up. The
`matcherOpSymbol` / `matcherOp` operator-renderers were a third
identical pair. This ADR introduces `internal/matcher` (peer to
`internal/backend` and `internal/config`) as the home for both
parsers and the operator renderer, returning `backend.Matcher`
directly so the cmd-side `promMatcher` value type disappears
rather than relocates.

Public surface: `ParseOne(string) (backend.Matcher, error)` for the
single-line `--matcher` flag path, `Parse(string) ([]backend.Matcher,
error)` for the multi-line silence-form textarea path (blank-line-
skipping plus `line N:` error wrap), and `Op(backend.Matcher) string`
for the operator renderer both callers reach for when summarising a
matcher slice back to text. Errors are sentinels (`ErrMissingOperator`,
`ErrIncompleteMatcher`) carrying the same user-facing strings the
two ad-hoc parsers used so the existing `line N: missing operator
(=, !=, =~, !~)` form-validation messages and the
`--matcher: missing operator …` cli-error wrap stay byte-stable.
The sentinel shape lets callers match with `errors.Is` instead of
substring-comparing the message text.

No `Render` is exported. The two callers want different shapes —
cmd summarises as `name<op>"value"` joined with commas (quoted
value, the Prom round-trip form); the form renders as `name<op>value`
joined with newlines (bare value, what the user types into the
textarea). Both consume `Op(m)` to pick the operator and keep
their own tiny renderer; a unified `Render` would either bake one
flavour and force the other site to keep its renderer anyway, or
take a flags struct that is more boilerplate than the inline
`m.Name + matcher.Op(m) + m.Value` it replaces.

`ParseOne` is liberal about quoting: a single layer of balanced
double quotes around the value is stripped, matching the prior
cmd-side behaviour and a strict superset of the form-side (the
form previously rejected `severity="critical"` lines by trapping
the quotes inside the value; the new behaviour accepts both and
round-trips correctly through `Parse` because `formatMatchers`
writes bare). This intentionally aligns the two surfaces on the
more lenient parser — operators copy-paste a `--matcher='…'`
invocation into the textarea and the same matcher results.

Considered and rejected: (a) parking the parser in
`internal/backend` next to `backend.Matcher` — `backend` is the
wire-shape boundary (HTTP clients, types matching the AM v2
schema), and a text-form parser bolted on would invert the
direction the package currently points; (b) keeping the
duplication on grounds of "two callers, bounded scope" — that
was the v0.0.1 stance recorded in the cmd-side comment, but the
two-char tie-breaker is a non-obvious invariant the next bug
report will land on first, and a second drift between the two
parsers is the kind of cleanup-pass regression the
pre-opensource gate is meant to catch; (c) generic
`Parse[Op](string) ([]Op, error)` to leave room for a future
PromQL-style operator set — speculative scope, no caller asking,
and adds a type parameter to the package's entire surface for
zero current benefit; (d) re-homing the form-level fuzz target
(`internal/tui/form/silence/fuzz_test.go`) into `internal/matcher` —
inspecting the target shows it drives the full silence form
state machine (six fields, key codes, resize events), not the
matcher parser specifically. The form fuzzer stays where it is;
the matcher package gets table-driven unit coverage that
exercises the operator tie-breaker, the leftmost-position rule,
quote-stripping, the multi-line wrap, and the sentinel-error
contract.
