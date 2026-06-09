# 0047 — Drop the route-grouped groups page

The `groups` page rendered Alertmanager's route `group_by` clustering
(`/api/v2/alerts/groups`) as an expand/collapse tree. ADR 0040 kept it
as a "complementary lens" — but that was when the alerts page was a
flat **alert instance** list and `groups` was the only view that
grouped. Now that L1 aggregates by alertname, both pages group the same
instances, and the route-batching lens has never been reached for in
real use: it answers a config-authoring question ("how will AM batch
notifications?"), not a triage one. This ADR records dropping the page
and excising its backend plumbing entirely.

## Status

Supersedes the "the route-grouped `groups` page is left untouched"
conclusion of ADR 0040 (the alertname-aggregate decision itself
stands). Shrinks the backend client surface documented in ADR 0028.

## Consequences

- **Full excision, not page-only.** `internal/tui/page/groups/` is
  deleted; `ListAlertGroups` and `backend.AlertGroup` are removed from
  the client interface, the multi fan-out, the vanilla implementation
  (wire / convert / read), and the test stub; the resolver loses
  `:groups` / `:gr` and the poller loses the groups feed. The
  `/api/v2/alerts/groups` surface leaves the codebase.
- **`groupdetail` (L2) is untouched.** It is the alerts drill-down's
  group-detail page (L1 → L2 → L3), polls the **alerts** feed, and
  never depended on `AlertGroup`. The "group detail" vocabulary in
  CONTEXT.md stays; only the route-based "alert group" term retires.
- **The route-group common-label silence prefill is lost.** Accepted:
  it is a convenience, not a capability (every silence form accepts
  free-form matchers), brittle to label drift (the reason ADR 0040
  rejected intersection-silence for the alertname aggregate), and
  unused in practice.
- **Loading affordance / refresh countdown** now render on two list
  pages (alerts/silences), not three.

## Considered and rejected

- **Reframe `groups` as a routing/notification-introspection lens**
  (route path, receiver, "who gets paged for this") — rejected as new
  surface area built on a need never felt. The routing question is
  answered in the Alertmanager config loop (YAML + reload), not a
  triage TUI; building the page would be a speculative feature.
- **Page-only removal, leaving `ListAlertGroups` as latent capability**
  — rejected. With no reframe planned it is dead code whose only role
  is to puzzle a future reader; clean-codebase and no-speculative-
  surface both say delete it.
- **Keep as-is** — rejected. As built it is a half-triage surface (no
  STATE breakdown, no AGE, no marks) that loses to the alerts page on
  triage, and for the common `group_by: [alertname]` it degenerates
  into a strictly-worse alerts page.
