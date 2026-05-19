// SPDX-License-Identifier: Apache-2.0

package cmd

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

// PageEnv bundles the shared dependencies every TUI page needs at
// construction time. It is built once in runTUI and read by every
// newXxxPage factory; passing one PageEnv keeps newResolver's
// parameter list bounded and makes adding a future shared dep a
// struct-field change instead of an N-arg propagation across two
// call sites (newResolver and the startup home factory).
//
// Package-internal construction concern. Consumers outside cmd/
// should not depend on it.
type PageEnv struct {
	EditorCtx           context.Context //nolint:containedctx // construction-time plumbing for page BulkCtx / SubmitCtx fields, not session state.
	Styles              *theme.Styles
	Scope               string
	SilenceClients      map[string]silenceform.Client
	SilenceWriteClients map[string]silences.Client
	Creator             string
	TenantRows          []tenant.Row
	Config              *config.Config
	Clients             map[string]backend.Client
	TimeFormat          func() timerender.Format
	ReadOnly            bool
	TenantNames         []string
	TenantConfigByName  map[string]config.Backend
	EditorResolver      edit.Resolver
}

func newAlertsPage(env *PageEnv, stateFilter, filter string) app.Page {
	return alerts.New(alerts.Options{
		Styles:             env.Styles,
		Now:                time.Now,
		Scope:              env.Scope,
		Clients:            env.SilenceClients,
		Creator:            env.Creator,
		TimeFormat:         env.TimeFormat(),
		BulkConcurrency:    env.Config.Defaults.BulkConcurrencyOrDefault(),
		Logger:             slog.Default(),
		ReadOnly:           env.ReadOnly,
		BulkCtx:            env.EditorCtx,
		SubmitCtx:          env.EditorCtx,
		InitialStateFilter: stateFilter,
		InitialFilter:      filter,
		Tenants:            env.TenantNames,
	})
}

func newSilencesPage(env *PageEnv) app.Page {
	return silences.New(silences.Options{
		Styles:          env.Styles,
		Now:             time.Now,
		Clients:         env.SilenceWriteClients,
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

func newGroupsPage(env *PageEnv) app.Page {
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

func newReceiversPage(env *PageEnv) app.Page {
	return receivers.New(receivers.Options{
		Styles:  env.Styles,
		Tenants: env.TenantNames,
	})
}

func newStatusPage(env *PageEnv) app.Page {
	return status.New(env.Styles, env.Scope)
}

func newTenantPage(env *PageEnv, drill func(string) (app.Page, error)) app.Page {
	p := tenant.New(tenant.Options{
		Styles:       env.Styles,
		DrillFactory: drill,
	})
	p.SetRows(env.TenantRows)
	return p
}

func newTenantConfigPage(env *PageEnv, name string) (app.Page, error) {
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
