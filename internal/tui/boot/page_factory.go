// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/alerts"
	"github.com/wilfriedroset/a10r/internal/tui/page/groups"
	"github.com/wilfriedroset/a10r/internal/tui/page/receivers"
	"github.com/wilfriedroset/a10r/internal/tui/page/silences"
	"github.com/wilfriedroset/a10r/internal/tui/page/status"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenant"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenantconfig"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// pageEnv bundles the shared dependencies every TUI page needs at
// construction time. Built once in Build and read by every
// newXxxPage factory; passing one pageEnv keeps the resolver's
// parameter list bounded and makes adding a future shared dep a
// struct-field change instead of an N-arg propagation across two
// call sites (the resolver and the startup home factory).
type pageEnv struct {
	EditorCtx          context.Context //nolint:containedctx // construction-time plumbing for page BulkCtx / SubmitCtx fields, not session state.
	Styles             *theme.Styles
	Scope              string
	SilenceClients     map[string]silenceform.Client
	Creator            string
	TenantRows         []tenant.Row
	Config             *config.Config
	Clients            map[string]backend.Client
	TimeFormat         func() timerender.Format
	ReadOnly           bool
	TenantNames        []string
	TenantConfigByName map[string]config.Backend
	EditorResolver     edit.Resolver
}

func newAlertsPage(env *pageEnv, stateFilter, filter string) app.Page {
	return alerts.New(alerts.Options{
		Styles:             env.Styles,
		Now:                time.Now,
		Scope:              env.Scope,
		Clients:            env.SilenceClients,
		Creator:            env.Creator,
		EditorResolver:     env.EditorResolver,
		TimeFormat:         env.TimeFormat(),
		BulkConcurrency:    env.Config.Defaults.BulkConcurrencyOrDefault(),
		Logger:             slog.Default(),
		ReadOnly:           env.ReadOnly,
		EditorCtx:          env.EditorCtx,
		BulkCtx:            env.EditorCtx,
		SubmitCtx:          env.EditorCtx,
		InitialStateFilter: stateFilter,
		InitialFilter:      filter,
		Tenants:            env.TenantNames,
	})
}

func newSilencesPage(env *pageEnv) app.Page {
	return silences.New(silences.Options{
		Styles:          env.Styles,
		Now:             time.Now,
		Clients:         env.SilenceClients,
		Creator:         env.Creator,
		EditorResolver:  env.EditorResolver,
		TimeFormat:      env.TimeFormat(),
		BulkConcurrency: env.Config.Defaults.BulkConcurrencyOrDefault(),
		Logger:          slog.Default(),
		ReadOnly:        env.ReadOnly,
		EditorCtx:       env.EditorCtx,
		BulkCtx:         env.EditorCtx,
		SubmitCtx:       env.EditorCtx,
		Tenants:         env.TenantNames,
	})
}

func newGroupsPage(env *pageEnv) app.Page {
	return groups.New(groups.Options{
		Styles:    env.Styles,
		Now:       time.Now,
		Clients:   env.SilenceClients,
		Creator:   env.Creator,
		ReadOnly:  env.ReadOnly,
		Tenants:   env.TenantNames,
		SubmitCtx: env.EditorCtx,
	})
}

func newReceiversPage(env *pageEnv) app.Page {
	return receivers.New(receivers.Options{
		Styles:  env.Styles,
		Tenants: env.TenantNames,
	})
}

func newStatusPage(env *pageEnv) app.Page {
	return status.New(env.Styles, env.Scope)
}

func newTenantPage(env *pageEnv, drill func(string) (app.Page, error)) app.Page {
	p := tenant.New(tenant.Options{
		Styles:       env.Styles,
		DrillFactory: drill,
	})
	p.SetRows(env.TenantRows)
	return p
}

func newTenantConfigPage(env *pageEnv, name string) (app.Page, error) {
	be, ok := env.TenantConfigByName[name]
	if !ok {
		return nil, fmt.Errorf("backend %q not in config", name)
	}
	fetcher, ok := env.Clients[name]
	if !ok {
		return nil, fmt.Errorf("backend %q failed to build at startup — fix a10r.yaml and restart", name)
	}
	return tenantconfig.New(tenantconfig.Options{
		Tenant:   name,
		Backend:  be,
		Fetcher:  fetcher,
		Styles:   env.Styles,
		FetchCtx: env.EditorCtx,
	}), nil
}
