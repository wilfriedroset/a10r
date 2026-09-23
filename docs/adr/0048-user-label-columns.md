# 0048 — User-declared label columns

The alerts and group-detail tables render a fixed column list, so an
operator who triages by `cluster` or `namespace` reads that label out
of the detail page one row at a time. A `columns:` list under `pages:`
lets the config add label-sourced columns to both tables, with an
optional `Shift+<letter>` sort key and an optional second tier of
columns behind `Shift+W`.

## Status

Extends the sort-namespace rule of ADR 0043: a user column's
`sort_key` claims a `Shift+<letter>` slot, so the loader rejects a key
already bound to a built-in sort or a view verb on that page. Does not
touch the alertname-aggregate decision of ADR 0040.

## Consequences

- **An aggregate cell shows agreement, never a sample.** On the alerts
  page a row is an alertname aggregate, so a label can hold several
  values. The cell prints the value when every instance agrees and
  `<N values>` when they do not. An instance missing the label counts
  as one distinct value.
- **No `replace` mode, no reordering.** Built-in columns carry the
  drill and silence semantics of ADR 0040, so the config can add
  columns but cannot remove or move them. User columns land between
  ALERTNAME and COUNT on alerts, and between INSTANCE and STATE on
  group detail.
- **`Shift+W` toggles the wide tier.** Lowercase `w` is the watch
  toggle on every list page and stays. While the tier is hidden, a
  sort on a hidden column is held pending: the header shows no arrow,
  `h`/`l` skip the column, and the sort returns with the tier.
- **A too-narrow row scrolls, it does not drop columns.** Left and
  Right move the viewport by one column with the first data column
  pinned, and the header marks the clipped edge. Dropping a column the
  user asked for would be a silent lie about the data on screen. An
  offset does not outlive the column set it was measured against, so
  `Shift+W` returns the row to the pinned left edge.
- **A column is never narrower than its own header.** The arrow beside
  a header is the whole direction contract, so a header cut down to
  fit would take the contract with it. A configured `width` therefore
  bounds the cells, not the column: the column is the wider of `width`
  and its own header plus the arrow.
- **Columns are text and display-only.** No typed columns, no
  annotations, no `align`, nothing tagged Dangerous. Every label value
  is a string, so byte-wise comparison is the whole sort contract, with
  `<N values>` after plain values and empty cells last.

## Considered and rejected

- **Show the first instance's value on an aggregate row** — rejected.
  It hides disagreement at the moment disagreement matters most, and a
  triage read of "cluster: prod" that silently drops three other
  clusters is worse than no column.
- **Drop columns automatically when the terminal is narrow** —
  rejected in favour of horizontal scroll. A clipped-edge marker tells
  the truth; a vanished column does not.
- **Extend user columns to the silences page** — rejected for now.
  Silences carry matchers, not labels, and the mapping is not one to
  one.
