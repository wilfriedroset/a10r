// SPDX-License-Identifier: Apache-2.0

// Command smoke is a manual integration harness for the
// internal/backend layer. It connects to a running Alertmanager,
// calls every read endpoint, and (unless -read-only is set)
// round-trips a CreateSilence → GetSilence → ExpireSilence cycle.
// Used after `make am-up` to verify the backend layer end-to-end
// before milestone tags. Not part of the released binary —
// goreleaser's `main: .` builds only the root package.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	a10rlog "github.com/wilfriedroset/a10r/internal/log"
)

func main() {
	// realMain returns an exit code; main wraps it so deferred cleanup
	// (logger Close) runs before os.Exit. gocritic.exitAfterDefer
	// would flag the alternative (defer + os.Exit in the same scope).
	os.Exit(realMain())
}

func realMain() int {
	url := flag.String("url", "http://localhost:9093", "Alertmanager base URL")
	readOnly := flag.Bool("read-only", false, "skip silence create/expire round-trip")
	flag.Parse()

	logger, closer, err := a10rlog.New(a10rlog.Opts{
		Format: a10rlog.FormatLogfmt,
		Level:  slog.LevelInfo,
		Stderr: true,
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "log init:", err)
		return 1
	}
	defer func() { _ = closer.Close() }()

	if err := run(context.Background(), logger, *url, *readOnly); err != nil {
		logger.Error("smoke failed", slog.String("err", err.Error()))
		return 1
	}
	logger.Info("smoke passed")
	return 0
}

func run(ctx context.Context, logger *slog.Logger, url string, readOnly bool) error {
	client, err := factory.Build(config.Backend{Name: "smoke", URL: url})
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}

	if err := exerciseReads(ctx, logger, client); err != nil {
		return err
	}
	if readOnly {
		return nil
	}
	return exerciseWrites(ctx, logger, client)
}

func exerciseReads(ctx context.Context, logger *slog.Logger, client backend.Client) error {
	status, err := client.Status(ctx)
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	logger.Info("status",
		slog.String("cluster", status.Cluster.Status),
		slog.Int("peers", len(status.Cluster.Peers)),
		slog.String("version", status.Version.Version),
		slog.Duration("uptime", status.Uptime),
	)

	receivers, err := client.ListReceivers(ctx)
	if err != nil {
		return fmt.Errorf("list receivers: %w", err)
	}
	names := make([]string, 0, len(receivers))
	for _, r := range receivers {
		names = append(names, r.Name)
	}
	logger.Info("receivers", slog.Int("count", len(receivers)), slog.Any("names", names))

	alerts, err := client.ListAlerts(ctx, backend.AlertFilter{})
	if err != nil {
		return fmt.Errorf("list alerts: %w", err)
	}
	logger.Info("alerts", slog.Int("count", len(alerts)))

	silences, err := client.ListSilences(ctx, backend.SilenceFilter{})
	if err != nil {
		return fmt.Errorf("list silences: %w", err)
	}
	logger.Info("silences", slog.Int("count", len(silences)))

	groups, err := client.ListAlertGroups(ctx, backend.AlertFilter{})
	if err != nil {
		return fmt.Errorf("list alert groups: %w", err)
	}
	logger.Info("alert_groups", slog.Int("count", len(groups)))

	return nil
}

func exerciseWrites(ctx context.Context, logger *slog.Logger, client backend.Client) error {
	now := time.Now().UTC()
	spec := backend.SilenceSpec{
		Matchers: []backend.Matcher{
			{Name: "alertname", Value: "A10rSmokeTest", IsEqual: true},
		},
		StartsAt:  now,
		EndsAt:    now.Add(5 * time.Minute),
		CreatedBy: "a10r-smoke",
		Comment:   "delete me — created by a10r smoke harness",
	}

	id, err := client.CreateSilence(ctx, spec)
	if err != nil {
		return fmt.Errorf("create silence: %w", err)
	}
	logger.Info("silence created", slog.String("id", id))

	fetched, err := client.GetSilence(ctx, id)
	if err != nil {
		return fmt.Errorf("get silence %q: %w", id, err)
	}
	logger.Info("silence fetched",
		slog.String("id", fetched.ID),
		slog.String("state", string(fetched.State)),
		slog.String("created_by", fetched.CreatedBy),
		slog.Int("matchers", len(fetched.Matchers)),
	)

	if err := client.ExpireSilence(ctx, id); err != nil {
		return fmt.Errorf("expire silence %q: %w", id, err)
	}
	logger.Info("silence expired", slog.String("id", id))

	// Verify expiration via a fresh GET. AM may take a moment to
	// transition state but the silence should always be readable.
	again, err := client.GetSilence(ctx, id)
	if err != nil {
		return fmt.Errorf("re-fetch silence %q: %w", id, err)
	}
	if again.State != backend.SilenceStateExpired {
		return errors.New("expected silence state 'expired' after ExpireSilence, got " + string(again.State))
	}
	logger.Info("silence state confirmed expired", slog.String("id", id))

	return nil
}
