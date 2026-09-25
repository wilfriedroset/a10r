// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
)

// The three answers a reload can give a field. A field added to the
// config later fails TestReloadFieldsArePartitioned until someone
// decides which answer it gets.
var (
	// reloadLive fields reach the running session through
	// Session.Apply, a page reading it at the point of use, or a
	// ConfigReloadedMsg.
	reloadLive = []string{
		"Config.Theme", "Config.Keys", "Config.Guardrails", "Config.Pages", "Config.Sources",
		"Defaults.PollInterval", "Defaults.ReadOnly", "Defaults.BulkConcurrency",
		"TUI.Tips", "TUI.TipsInterval", "TUI.TerminalTitle", "TUI.PollDelta", "TUI.Notify",
		"Backend.ReadOnly", "Backend.PollInterval",
	}
	// reloadFrozen fields refuse the whole reload: see
	// frozenConfigChanged.
	reloadFrozen = []string{
		"Config.Log",
		"Defaults.LogFormat",
		"Backend.Name", "Backend.URL", "Backend.Prefix", "Backend.TenantHeader", "Backend.Tenant",
		"Backend.BasicAuth", "Backend.Authorization", "Backend.BearerToken", "Backend.Headers",
		"Backend.TLSConfig", "Backend.ProxyURL", "Backend.NoProxy", "Backend.ProxyFromEnvironment",
		"Backend.RemoteTimeout", "Backend.Capabilities",
	}
	// reloadRestart fields apply nothing mid-session and are named in
	// the flash: see restartFields.
	reloadRestart = []string{"TUI.Remember"}
)

func TestReloadFieldsArePartitioned(t *testing.T) {
	t.Parallel()

	// Config.Defaults, Config.TUI and Config.Backends are walked
	// rather than classified: their fields answer differently.
	walked := map[reflect.Type]bool{
		reflect.TypeFor[config.Defaults]():  true,
		reflect.TypeFor[config.TUI]():       true,
		reflect.TypeFor[[]config.Backend](): true,
	}
	var fields []string
	for _, typ := range []reflect.Type{
		reflect.TypeFor[config.Config](),
		reflect.TypeFor[config.Defaults](),
		reflect.TypeFor[config.TUI](),
		reflect.TypeFor[config.Backend](),
	} {
		for f := range typ.Fields() {
			if !walked[f.Type] {
				fields = append(fields, typ.Name()+"."+f.Name)
			}
		}
	}

	seen := map[string]int{}
	for _, set := range [][]string{reloadLive, reloadFrozen, reloadRestart} {
		for _, name := range set {
			seen[name]++
		}
	}
	for _, name := range fields {
		require.Equal(t, 1, seen[name], "%s must sit in exactly one of live, frozen and restart", name)
		delete(seen, name)
	}
	require.Empty(t, seen, "a set names a field the config no longer has")
}
