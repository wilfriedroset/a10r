// SPDX-License-Identifier: Apache-2.0

package action

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistry_Empty(t *testing.T) {
	t.Parallel()

	r := New()
	require.Equal(t, 0, r.Len())
	require.Empty(t, r.All())
	require.Empty(t, r.Hints("alerts"))
	require.Empty(t, r.Filter(true))
	require.Empty(t, r.Filter(false))
}

func TestRegistry_RegisterAndAll(t *testing.T) {
	t.Parallel()

	r := New()
	r.Register(Action{Key: "s", Description: "silence alert", View: "alerts", Dangerous: true})
	r.Register(Action{Key: "?", Description: "help", View: ""})

	all := r.All()
	require.Len(t, all, 2)
	require.Equal(t, "s", all[0].Key)
	require.Equal(t, "?", all[1].Key)
}

func TestRegistry_AllReturnsCopy(t *testing.T) {
	t.Parallel()

	// Mutating the slice the caller receives must NOT affect the
	// registry. Pinning so a future return-by-shared-slice
	// optimisation is loud.
	r := New()
	r.Register(Action{Key: "a", View: "alerts"})

	out := r.All()
	out[0].Key = "mutated"

	require.Equal(t, "a", r.All()[0].Key,
		"mutating the All() return must not bleed back into the registry")
}

func TestRegistry_Hints_ScopesByView(t *testing.T) {
	t.Parallel()

	r := New()
	r.Register(Action{Key: "?", View: ""})             // global
	r.Register(Action{Key: "s", View: "alerts"})       // alerts only
	r.Register(Action{Key: "n", View: "silences"})     // silences only
	r.Register(Action{Key: "Shift+T", View: "alerts"}) // alerts only

	alertsHints := r.Hints("alerts")
	require.Len(t, alertsHints, 3, "alerts view sees its 2 + 1 global")
	keys := []string{alertsHints[0].Key, alertsHints[1].Key, alertsHints[2].Key}
	require.Equal(t, []string{"?", "s", "Shift+T"}, keys,
		"Hints preserves registration order")

	silencesHints := r.Hints("silences")
	require.Len(t, silencesHints, 2, "silences view sees its 1 + 1 global")
	require.Equal(t, "?", silencesHints[0].Key)
	require.Equal(t, "n", silencesHints[1].Key)
}

func TestRegistry_Hints_UnknownViewReturnsOnlyGlobals(t *testing.T) {
	t.Parallel()

	r := New()
	r.Register(Action{Key: "?", View: ""})
	r.Register(Action{Key: "s", View: "alerts"})

	out := r.Hints("nonexistent-view")
	require.Len(t, out, 1)
	require.Equal(t, "?", out[0].Key)
}

func TestRegistry_Filter_ReadOnlyDropsDangerous(t *testing.T) {
	t.Parallel()

	r := New()
	r.Register(Action{Key: "?", View: ""})
	r.Register(Action{Key: "s", View: "alerts", Dangerous: true})
	r.Register(Action{Key: "Shift+T", View: "alerts"})
	r.Register(Action{Key: "Ctrl+S", View: "alerts", Dangerous: true, Bulk: true})

	readOnly := r.Filter(true)
	require.Len(t, readOnly, 2, "two non-Dangerous actions")
	require.Equal(t, "?", readOnly[0].Key)
	require.Equal(t, "Shift+T", readOnly[1].Key)

	full := r.Filter(false)
	require.Len(t, full, 4, "Filter(false) returns everything")
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	t.Parallel()

	r := New()
	r.Register(Action{Key: "s", View: "alerts"})

	require.PanicsWithValue(t,
		`action.Registry: duplicate registration for view="alerts" key="s"`,
		func() {
			r.Register(Action{Key: "s", View: "alerts"})
		},
		"duplicate (view, key) must panic at startup so the bug surfaces in dev rather than runtime")
}

func TestRegistry_SameKeyDifferentViewIsAllowed(t *testing.T) {
	t.Parallel()

	// `s` is silence-from-alert on the alerts list AND on the alert
	// detail page. Both must register without conflict — the
	// (view, key) pair is what's unique, not key alone.
	r := New()
	r.Register(Action{Key: "s", View: "alerts"})
	require.NotPanics(t, func() {
		r.Register(Action{Key: "s", View: "alert"})
	})
	require.Equal(t, 2, r.Len())
}

func TestRegistry_GlobalAndViewSameKeyIsAllowed(t *testing.T) {
	t.Parallel()

	// Different views are different keys — global ("") and "alerts"
	// are distinct from the dispatcher's perspective. The duplicate
	// check is per (view, key), so global "s" and view-scoped "s"
	// coexist; the dispatcher's precedence rules pick the winner.
	r := New()
	r.Register(Action{Key: "s", View: ""})
	require.NotPanics(t, func() {
		r.Register(Action{Key: "s", View: "alerts"})
	})
}
