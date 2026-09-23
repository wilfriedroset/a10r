// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"slices"
	"strings"
)

// Column declares one label-sourced table column on a list page. The
// value is read from the alert's labels, so every cell is text — see
// ADR 0048 for why there are no typed columns and no `replace` mode.
type Column struct {
	Label string `yaml:"label"`
	// Title overrides the rendered header. Empty means the
	// upper-cased Label.
	Title string `yaml:"title,omitempty"`
	// SortKey is the uppercase letter that binds Shift+<letter> to
	// this column. Empty means the column is not sortable at all:
	// the h/l walk skips it rather than making a column the operator
	// declared unsortable the active sort.
	SortKey string `yaml:"sort_key,omitempty"`
	// Width sizes the column at a fixed cell count, never narrower
	// than its own header, plus the sort arrow when SortKey is set:
	// the arrow is the whole direction contract (ADR 0048), so the
	// width sizes the cells rather than cutting the title. Zero
	// measures the widest cell in view, as the built-in columns do.
	Width int `yaml:"width,omitempty"`
	// Wide hides the column until the user presses Shift+W.
	Wide bool `yaml:"wide,omitempty"`
}

// TitleOrDefault returns the rendered header label: the configured
// Title upper-cased, or the upper-cased Label when Title is empty.
func (c Column) TitleOrDefault() string {
	if t := strings.TrimSpace(c.Title); t != "" {
		return strings.ToUpper(t)
	}
	return strings.ToUpper(strings.TrimSpace(c.Label))
}

// minColumnWidth is the floor for an explicit `width`. Below three
// cells a value is an ellipsis and at most one character, which
// carries no information.
const minColumnWidth = 3

// columnPage is one table page's column-validation context.
type columnPage struct {
	path     string
	name     string
	titles   []string
	reserved string
}

// pageAlerts and pageGroupDetail reserve the same letters today (both
// pages carry marks, a state filter, a state format toggle, and the
// Shift+W wide tier), but they are spelled out separately because the
// binding sets are page-local and free to diverge. G is in the set
// although no page binds it as a sort: it is the jump-to-bottom
// motion, which the cursor consumes before the sorter ever sees the
// key, so a user column on G would be silently dead.
var (
	pageAlerts = columnPage{
		path:     "pages.alerts",
		name:     "alerts",
		titles:   []string{"TENANT", "SEVERITY", "ALERTNAME", "COUNT", "STATE", "AGE"},
		reserved: "ACFGNSTVW",
	}
	pageGroupDetail = columnPage{
		path:     "pages.group_detail",
		name:     "group detail",
		titles:   []string{"SEVERITY", "INSTANCE", "STATE", "AGE"},
		reserved: "ACFGNSTVW",
	}
)

// validateColumns enforces the fail-closed column schema of ADR 0048.
// The first error wins, matching Config.Validate's one-problem-at-a-
// time posture.
func validateColumns(page columnPage, cols []Column) error {
	labels := make(map[string]struct{}, len(cols))
	keys := make(map[string]struct{}, len(cols))
	titles := make(map[string]struct{}, len(cols))

	for i, c := range cols {
		if err := validateColumnLabel(page, i, c.Label, labels); err != nil {
			return err
		}
		if err := validateColumnSortKey(page, i, c.SortKey, keys); err != nil {
			return err
		}
		if c.Width != 0 && c.Width < minColumnWidth {
			return fmt.Errorf("%s.columns[%d].width: %d is below the minimum of %d",
				page.path, i, c.Width, minColumnWidth)
		}
		if err := validateColumnTitle(page, i, c.TitleOrDefault(), titles); err != nil {
			return err
		}
	}
	return nil
}

// validateColumnLabel rejects an empty or padded label. Padding is
// rejected rather than trimmed so the stored Label is always the
// exact key the renderer looks up in the alert's label map.
func validateColumnLabel(page columnPage, i int, label string, seen map[string]struct{}) error {
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("%s.columns[%d].label: must not be empty", page.path, i)
	}
	if label != strings.TrimSpace(label) {
		return fmt.Errorf("%s.columns[%d].label: %q must not have leading or trailing whitespace",
			page.path, i, label)
	}
	if _, dup := seen[label]; dup {
		return fmt.Errorf("%s.columns[%d].label: %q is already declared on the %s page",
			page.path, i, label, page.name)
	}
	seen[label] = struct{}{}
	return nil
}

// validateColumnTitle rejects a header that shadows a built-in or
// repeats another user column's. Two columns under one header are as
// ambiguous as a column with no header at all.
func validateColumnTitle(page columnPage, i int, title string, seen map[string]struct{}) error {
	if slices.Contains(page.titles, title) {
		return fmt.Errorf("%s.columns[%d].title: %q is a built-in column title on the %s page",
			page.path, i, title, page.name)
	}
	if _, dup := seen[title]; dup {
		return fmt.Errorf("%s.columns[%d].title: %q is already used by another column on the %s page",
			page.path, i, title, page.name)
	}
	seen[title] = struct{}{}
	return nil
}

// validateColumnSortKey rejects a key a built-in verb, a built-in
// sort, a vim motion, or another user column already answers. A
// reserved key is rejected rather than shadowed because the page
// handles its own bindings first, so the column would never sort.
func validateColumnSortKey(page columnPage, i int, key string, seen map[string]struct{}) error {
	if key == "" {
		return nil
	}
	if len(key) != 1 || key[0] < 'A' || key[0] > 'Z' {
		return fmt.Errorf("%s.columns[%d].sort_key: %q must be one uppercase ASCII letter",
			page.path, i, key)
	}
	_, dup := seen[key]
	if dup || strings.Contains(page.reserved, key) {
		return fmt.Errorf("%s.columns[%d].sort_key: %q is already bound on the %s page",
			page.path, i, key, page.name)
	}
	seen[key] = struct{}{}
	return nil
}
