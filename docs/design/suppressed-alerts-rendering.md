# Suppressed alerts: rendering plan

Status: ready to implement. Self-contained; do not replay the originating conversation to act on this.

## Goal

Surface *why* an alert is `suppressed` in the TUI, in two places:

1. **Alerts list** (`internal/tui/page/alerts/alerts.go`): dim the foreground of suppressed rows, k9s-style ("Completed pod" treatment).
2. **Alert describe** (`internal/tui/page/alert/alert.go`): add a `Suppression:` block listing silence IDs, inhibitor fingerprints, and mute-time-interval names — whichever apply.

Alertmanager has three suppression mechanisms, all reported on `/api/v2/alerts` under `status.{silencedBy,inhibitedBy,mutedBy}`:

- `silencedBy`: list of silence IDs (explicit user-created mute).
- `inhibitedBy`: list of fingerprints of alerts whose firing inhibits this one (per `inhibit_rules` config).
- `mutedBy`: list of mute_time_interval names (per route `mute_time_intervals` config).

## Settled design decisions

- **Precedence in the list row style.** Cursor → marked → dimmed. Cursor wraps fg+bg (unchanged). Marked tints fg only (unchanged). Dimmed is a third arm that fires when `state == suppressed` and the row is neither cursor nor marked. Marked beats dimmed on purpose: marked is an explicit user action, suppression is ambient state.
- **Scope: v0-strict only.** Render raw silence IDs and raw inhibitor fingerprints. Do **not** resolve silences to creator/comment, do **not** look up inhibitor fingerprints against the list snapshot. v1 (resolution + nicety) is a separate, later piece of work.
- **Section label** in the describe view is `Suppression:` (parallels `Labels:` / `Annotations:`).
- **Include `MutedBy` plumbing** in this work so all three suppression mechanisms are covered. Cost is small and shipping with two of three is a paper-cut.
- **Title and HeaderContent in the describe page stay bright.** `Title()` is a pure breadcrumb (`Describe(<scope>/<alertname>)`); `HeaderContent()` is the navigation strip (`alertname · state · tenant`). Both are nav cues, not state surfaces — dimming them would feel wrong. The dimming applies to list rows only.

## Verifications already done

- Vanilla Alertmanager v2 emits `mutedBy` on `/api/v2/alerts`. Confirmed in `prometheus/alertmanager/api/v2/openapi.yaml:512`. The field is `required` alongside `silencedBy` and `inhibitedBy`.
- All 5 skins under `internal/tui/theme/skins/*.yaml` already define `table.dimmed: { fg: overlay, bg: base }`. No skin backfill needed.
- `theme.Styles.Table.Dimmed` is declared at `internal/tui/theme/styles.go:58` and currently unused — this work activates it.
- `Alert.SilencedBy` and `Alert.InhibitedBy` already exist in the domain model (`internal/backend/types.go:47-48`) and are populated from the wire (`internal/backend/vanilla/wire.go:31-32`, `convert.go:24-25`). `MutedBy` is missing in all three places.
- Describe page does **not** currently take a backend client; it operates on a snapshot. v0-strict needs no new dependency. v1 will need to add a `Reader` (or a `SilenceLookup` callback) to `alert.Options` — out of scope here.

## Commit sequence

Three commits, in order. Each is independently testable, independently reviewable, and prek-clean. Conventional-commit subjects below; expand body with the *why* per project policy.

### Commit 1 — `feat(internal/backend): plumb MutedBy through alert wire/domain`

Files:

- `internal/backend/types.go` — add `MutedBy []string` to `Alert` struct (line 39 area; place it after `InhibitedBy` at line 48 to keep the three suppression-reason fields grouped).
- `internal/backend/vanilla/wire.go` — add `MutedBy []string \`json:"mutedBy,omitempty"\`` to `wireAlertStatus` (line 29-33). Match the existing tag style used by SilencedBy/InhibitedBy.
- `internal/backend/vanilla/convert.go` — in `toAlert` (line 15), map `MutedBy: w.Status.MutedBy` next to the existing `SilencedBy` / `InhibitedBy` lines (24-25).

Tests:

- Find the existing `toAlert` round-trip test (likely `internal/backend/vanilla/convert_test.go` or similar). Add a fixture that includes `mutedBy: ["out-of-hours", "weekends"]` and assert the field is propagated.
- If a JSON-decode test exists for `wireAlert`, add a case with `"status": {"state":"suppressed","silencedBy":[],"inhibitedBy":[],"mutedBy":["out-of-hours"]}` and assert decode succeeds and the field round-trips.
- TDD per project policy: write the test first, watch it fail, then add the field.

Out of scope for this commit: no rendering changes, no Mimir-side adjustments (Mimir wraps vanilla so it inherits automatically).

### Commit 2 — `feat(internal/tui/page/alerts): dim suppressed rows in the list`

File: `internal/tui/page/alerts/alerts.go`, function `renderRows` (line 595).

Change: extend the style switch at lines 637-644 with a third arm. Final shape:

```go
switch {
case i == p.cursor:
    line = p.styles.Table.Cursor.Render(line)
case marked:
    line = lipgloss.NewStyle().
        Foreground(p.styles.Table.Marked.GetForeground()).
        Render(line)
case a.State == backend.AlertStateSuppressed:
    line = p.styles.Table.Dimmed.Render(line)
}
```

Note the comment block at lines 630-635 documents the cursor/marked precedence. Update it to mention the dimmed arm and why marked beats dimmed.

Tests: `internal/tui/page/alerts/alerts_test.go`.

- The existing `stripStyle` helper (lines 26-41) erases ANSI before assertions, which is the right pattern for content tests but wrong for proving styling actually fires. Add one focused test that operates on the *un-stripped* output: render a row whose alert has `state == suppressed`, then assert the row contains the SGR sequence for `Table.Dimmed`'s foreground colour (or, more robustly, render the same string through `p.styles.Table.Dimmed.Render(...)` and assert the rendered row contains that exact prefix).
- Add a second test: a suppressed alert that is *also* marked — assert the row is rendered with the marked colour (i.e. dimmed branch did NOT fire), proving precedence.
- Add a third test: a suppressed alert that is *also* the cursor — assert the cursor style fires, proving cursor still beats dimmed.
- Keep the existing content assertions (alertname presence etc.) using the stripped form — those still pass and document the table layout.

Manual smoke: launch the TUI against a backend with at least one silenced alert, eyeball that the row dims and that scrolling/marking/cursor still feel right.

### Commit 3 — `feat(internal/tui/page/alert): render Suppression block in describe`

File: `internal/tui/page/alert/alert.go`, function `bodyLines` (line 230).

Insert a new section after the Generator URL block (after line 240) when `p.a.State == backend.AlertStateSuppressed`. Layout (matches the `kvLines`-style indented k/v under each section header):

```
Suppression:
  silenced by:  abc-123-def, 456-ghi-789
  inhibited by: 0006251c575c1dd0
  muted by:     out-of-hours
```

Rules:

- Header line is exactly `Suppression:` (singular; consistent with `Labels:` / `Annotations:`).
- Sub-rows render only the buckets that are non-empty. If the alert is suppressed but all three lists are empty (shouldn't happen against vanilla AM, but be defensive — could happen under a non-conforming proxy or a bug upstream), render a single line:
  ```
    (no reason reported by Alertmanager)
  ```
  This is preferable to a bare `Suppression:` header with nothing under it, which would look like a render bug.
- Each ID/fingerprint/interval-name renders as-is. No truncation. No resolution. Comma-separated within a line.
- If the comma-separated list of IDs would overflow `width`, wrap with the same hanging indent the rest of the file uses (`wrapHanging` at `alert.go:317` area). Match the existing hang-column logic in `kvLines` (lines 292-315): hangCols = width of the `"  silenced by: "` prefix.

Tests: `internal/tui/page/alert/alert_test.go`.

- Extend `sample()` (line 73-88) to optionally accept suppression metadata, OR introduce a sibling helper `sampleSuppressed(silencedBy, inhibitedBy, mutedBy []string)` if extending `sample` would touch too many call sites. Read the file first and pick whichever is less invasive.
- Cases to cover:
  1. `state == active`: no Suppression block in output.
  2. `state == suppressed, silencedBy=["s1","s2"], others empty`: Suppression block appears, contains `silenced by: s1, s2`, no other sub-rows.
  3. `state == suppressed, inhibitedBy=["fp1"]`: only the inhibited-by row.
  4. `state == suppressed, mutedBy=["out-of-hours"]`: only the muted-by row.
  5. All three populated: all three rows in stable order (silenced, inhibited, muted).
  6. `state == suppressed` with all three empty: the defensive `(no reason reported by Alertmanager)` fallback line.
  7. Wrap behaviour: a long list of silence IDs at narrow width (e.g. width=40) wraps with hanging indent. Assert the second line starts with the right number of spaces.
- Use the existing `stripStyle` pattern for content assertions; styling isn't relevant in the describe view.

## Out of scope (deferred to a separate, later piece of work)

- Resolving silence IDs to `createdBy` / `comment` / `endsAt`. Requires `alert.Options` to grow a `SilenceLookup` (or a `Reader`), an async `Init()` cmd, a `silencesResolvedMsg` reducer, and wiring in `cmd/tui.go`. Track separately.
- Resolving inhibitor fingerprints to alertnames by cross-referencing the list snapshot. Requires passing the snapshot or a lookup func into the describe page. Track with the silence-resolution work.
- Dimming the alerts list `HeaderContent` strip when the focused alert is suppressed. Decision: keep it bright. If this is revisited, do it as its own atomic change.
- Surfacing suppression reasons in any view other than the list and the describe page (e.g. silence-list cross-reference, inhibit-rule visualiser). Not requested.

## Acceptance checks before merging the series

- `prek -a` clean across all three commits.
- Each commit's tests pass independently when checked out at that commit (project policy: atomic commits, one logical unit each, bisect-friendly).
- 80%+ coverage on the new code paths.
- Manual smoke against a real Alertmanager with a silenced alert and (if reachable) an inhibited alert and a `mute_time_intervals`-routed alert. The screenshot from the originating issue (a `ConsulMissingMasterNode` alert in `suppressed` state) is the canonical visual to compare against — the describe view should now show the reason, and the list row for the same alert should render dimmed.
