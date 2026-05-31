# 0040 — Alerts page aggregates by alertname

The alerts list was a flat table, one row per **alert instance**
(one fingerprint). When N instances share an alertname — 567 hosts
firing `HostProcessNeedRestart` — they bury every other alert and
are mutually indistinguishable, so triage degrades into drill-in /
`Esc` / drill-in across near-identical rows. This ADR records the
decision to make the alerts page an **aggregate** view: one row per
`(tenant, alertname)`, with a COUNT column, a per-state **state
breakdown** (`9 active · 3 suppressed`), max severity, and oldest
age, drilling down to a new per-alert **group detail** (L2) listing
the instances, and then to the existing single-instance **instance
detail** (L3, unchanged). The route-grouped `groups` page is left
untouched — it answers a different question (how Alertmanager
batches notifications by `group_by`) and remains the lens for that.

Grouping is on the `alertname` label **alone**, accepting a
one-directional looseness: two distinct upstream rules emitting the
same alertname within one tenant merge into one row (over-merge),
but a single rule never splits across rows (it sets one alertname).
This is forced by the data source, not a preference — a10r consumes
Alertmanager's `/api/v2/alerts`, which carries no rule identity.
Grafana's instance rollup keys on `__alert_rule_uid__`, but that
field only exists on Grafana's **ruler** surface (the Prometheus
rules API), not on the Alertmanager API we speak. Within a tenant,
colliding alertnames are rare and the operator generally wants them
together ("everything called HighLatency"); `(tenant, alertname)`
keeps tenants from cross-merging.

The visual north star (Grafana's "Instances" tab with "+N common
labels" and Firing/Pending toggles) is a **ruler-rules** surface,
not an Alertmanager one. Grafana's own Alertmanager view is flat,
route-grouped, with no alertname rollup. So this design deliberately
ports Grafana's *ruler* ergonomics onto Alertmanager data that
Grafana itself never presents that way. Two concrete consequences
of that mismatch: (a) the STATE column tallies AM-native states
`active` / `suppressed` / `unprocessed` only — there is **no
pending**, which is a rule-evaluation state we cannot observe, and
re-adding it would be a bug; (b) the "+N common labels" idea is
realised by intersecting labels across the group (the same
intersection the `groups` page already uses for its silence
prefill), with the common set shown once and each instance row
showing only its differing labels.

## Consequences

- **Three pushed levels, not two.** L1 list → L2 group detail → L3
  instance detail; `Esc` pops one level, matching a10r's page-stack
  idiom rather than a web-style inline accordion. L2 is a new but
  ordinary list page (poll-aware, cursor/marks/sort/filter) scoped
  to one `(tenant, alertname)`, plus a default-on common-labels
  strip; it is not a bespoke detail/list hybrid.
- **`Enter` on a COUNT==1 alert skips L2 → L3.** The single-instance
  case is the common one and a one-row L2 would be pure friction.
  The destination then depends on a count, so COUNT==1 rows carry a
  trailing `→` marker and L2/L3 keep a consistent keymap (`S` = open
  silences on both) so the skip can never land the same keystroke on
  a different verb.
- **`s` silences the cursor unit; scope follows altitude.** At L1 the
  unit is the alert, so `s` is **silence-all**, prefilled with
  `alertname=<name>` alone — the aggregate's identity, editable, and
  gated by the confirm modal whenever COUNT>1 (blast radius, not
  mark count, decides the gate). At L2/L3 the unit is an instance, so
  `s` is **silence-one** with the full label set. Silence-all has no
  L2 key (reached by `Esc` to L1) so `S` stays single-meaning.
- **Silence-all ignores the active filter.** `alertname=X` is
  prefilled regardless of any `/`-filter or state-filter narrowing
  the view; a persistent **scope note** in the form states the true
  scope and, when the source view was filtered, warns the filter is
  not applied. Whether silence-all should instead scope to the
  filtered instances is deferred pending real-world feedback — the
  scope note is where that behaviour would change.
- **Filters narrow instances, then groups rebuild from survivors;**
  empty groups drop. COUNT / STATE / AGE always describe the
  post-filter reality rather than a full count with a hidden filter.

## Considered and rejected

- **Keep the flat instance list** — the status quo; rejected because
  it is the reported pain (high-cardinality alerts bury everything
  and force repetitive drill-in/`Esc`).
- **Reuse / extend the route-grouped `groups` page** instead of a
  second rollup — rejected because route grouping answers a
  different question (notification batching) and is keyed on
  `group_by` labels, which are frequently *not* alertname. The two
  are complementary lenses, not duplicates.
- **Group by the full common-label set rather than alertname** —
  more surgical, but it makes the row identity depend on incidental
  shared labels, churns as labels drift, and has no stable name to
  show as the row's headline. Alertname is the operator's mental
  index.
- **Mirror Grafana's Firing/Pending state model** — rejected: those
  are ruler states absent from the Alertmanager API; surfacing a
  bucket we can never populate would be a standing lie.
- **Silence-all on the common-label intersection** (the `groups`
  page's approach) — rejected for the alertname aggregate because
  its identity is the alertname alone; baking incidental commons
  (`severity=critical`) into the matcher makes the silence
  surprisingly narrow and brittle to label drift. Both pages still
  follow one rule — prefill the matchers that define the group's
  identity — they just have different identities.
- **Inline-expand the instance list inside L2** (Grafana's web
  accordion) — rejected as a non-idiom for a page-stack TUI; pushing
  L3 reuses the existing instance-detail page wholesale.
