// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/report"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/alerts"
	"github.com/wilfriedroset/a10r/internal/tui/page/receivers"
	"github.com/wilfriedroset/a10r/internal/tui/page/selfreport"
	"github.com/wilfriedroset/a10r/internal/tui/page/silences"
	"github.com/wilfriedroset/a10r/internal/tui/page/status"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenant"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenantconfig"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
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
	EditorCtx      context.Context //nolint:containedctx // construction-time plumbing for page BulkCtx / SubmitCtx fields, not session state.
	Styles         *theme.Styles
	Scope          string
	SilenceClients map[string]silenceform.Client
	Creator        string
	TenantRows     []tenant.Row
	Config         *config.Config
	Clients        map[string]backend.Client
	TimeFormat     func() timerender.Format
	StateFormat    func() stateformat.Format
	ReadOnly       bool
	// Guardrails is the per-tenant write policy every write page
	// consults before it offers or runs a verb.
	Guardrails         guardrail.Set
	TenantNames        []string
	TenantConfigByName map[string]config.Backend
	EditorResolver     edit.Resolver
	Now                func() time.Time
	// SortMemory is the per-page remembered sort column, handed to
	// every page's Options. Never nil; a disabled store answers empty.
	SortMemory tablesort.Memory
	// InfoReport renders the `:info` body. Assigned after
	// buildPageEnv returns because it closes over startup facts the
	// env itself does not carry.
	InfoReport func() report.InfoInput
}

func newInfoPage(env *pageEnv) app.Page {
	return selfreport.New(selfreport.Options{
		Title: pageInfo,
		Render: func() string {
			// InfoReport is a two-phase init: Build assigns it after
			// buildPageEnv returns, so an env assembled anywhere else
			// (tests that only exercise the resolver) has it nil.
			if env.InfoReport == nil {
				return "(no info report wired)"
			}
			var buf strings.Builder
			if err := report.Info(&buf, env.InfoReport()); err != nil {
				return fmt.Sprintf("(failed to render the info report: %v)", err)
			}
			return buf.String()
		},
	})
}

func newAlertsPage(env *pageEnv, stateFilter, filter string) app.Page {
	return alerts.New(alerts.Options{
		Styles:             env.Styles,
		Now:                env.Now,
		Scope:              env.Scope,
		Clients:            env.SilenceClients,
		Creator:            env.Creator,
		EditorResolver:     env.EditorResolver,
		TimeFormat:         env.TimeFormat(),
		StateFormat:        env.StateFormat(),
		BulkConcurrency:    env.Config.Defaults.BulkConcurrencyOrDefault(),
		Logger:             slog.Default(),
		ReadOnly:           env.ReadOnly,
		Guardrails:         env.Guardrails,
		EditorCtx:          env.EditorCtx,
		BulkCtx:            env.EditorCtx,
		SubmitCtx:          env.EditorCtx,
		InitialStateFilter: stateFilter,
		InitialFilter:      filter,
		Tenants:            env.TenantNames,
		PollDelta:          env.Config.TUI.PollDelta,
		SortMemory:         env.SortMemory,
		Columns:            env.Config.Pages.Alerts.Columns,
		GroupDetailColumns: env.Config.Pages.GroupDetail.Columns,
	})
}

func newSilencesPage(env *pageEnv) app.Page {
	return silences.New(silences.Options{
		Styles:          env.Styles,
		Now:             env.Now,
		Clients:         env.SilenceClients,
		Creator:         env.Creator,
		EditorResolver:  env.EditorResolver,
		TimeFormat:      env.TimeFormat(),
		BulkConcurrency: env.Config.Defaults.BulkConcurrencyOrDefault(),
		Logger:          slog.Default(),
		ReadOnly:        env.ReadOnly,
		Guardrails:      env.Guardrails,
		EditorCtx:       env.EditorCtx,
		BulkCtx:         env.EditorCtx,
		SubmitCtx:       env.EditorCtx,
		Tenants:         env.TenantNames,
		SortMemory:      env.SortMemory,
	})
}

func newReceiversPage(env *pageEnv) app.Page {
	return receivers.New(receivers.Options{
		Styles:     env.Styles,
		Tenants:    env.TenantNames,
		SortMemory: env.SortMemory,
	})
}

func newStatusPage(env *pageEnv) app.Page {
	return status.New(env.Styles, env.Scope)
}

func newTenantPage(env *pageEnv, drill func(string) (app.Page, error)) app.Page {
	p := tenant.New(tenant.Options{
		Styles:       env.Styles,
		DrillFactory: drill,
		SortMemory:   env.SortMemory,
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
