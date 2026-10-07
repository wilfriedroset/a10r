// SPDX-License-Identifier: Apache-2.0

package log_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	a10rlog "github.com/wilfriedroset/a10r/internal/log"
)

func captureLogger(c *a10rlog.Capture) (*slog.Logger, *bytes.Buffer) {
	var sink bytes.Buffer
	inner := slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(c.Wrap(inner)), &sink
}

// The capture is a side-channel, not a filter: every record must still
// reach the file even while the window is open, because the log is the
// audit trail and the page is a convenience.
func TestCapture_PassesEveryRecordThrough(t *testing.T) {
	t.Parallel()

	c := &a10rlog.Capture{}
	logger, sink := captureLogger(c)

	c.Start()
	logger.Warn("during")
	c.Stop()
	logger.Warn("after")

	require.Contains(t, sink.String(), "during")
	require.Contains(t, sink.String(), "after")
}

// Only warnings and worse belong on the page. Debug and info records
// are the normal running commentary, and a page that listed them would
// bury the one line the operator opened it for.
func TestCapture_KeepsOnlyWarningsInsideTheWindow(t *testing.T) {
	t.Parallel()

	c := &a10rlog.Capture{}
	logger, _ := captureLogger(c)

	logger.Warn("before the window")
	c.Start()
	logger.Debug("noise")
	logger.Info("also noise")
	logger.Warn("unknown skin")
	logger.Error("backend unreachable")
	c.Stop()
	logger.Warn("after the window")

	require.Equal(t, []string{"unknown skin", "backend unreachable"}, c.Messages())
}

// A warning that names the offending value is only useful with the
// value attached: "unknown skin" alone sends the operator looking for
// which skin.
func TestCapture_AppendsTheRecordAttributes(t *testing.T) {
	t.Parallel()

	c := &a10rlog.Capture{}
	logger, _ := captureLogger(c)

	c.Start()
	logger.Warn("unknown skin",
		slog.String("requested", "nord"),
		slog.String("default", "catppuccin-mocha"),
	)
	c.Stop()

	require.Equal(t,
		[]string{"unknown skin (requested=nord, default=catppuccin-mocha)"},
		c.Messages(),
	)
}

// A second window answers `:reload`, which re-runs the loaders and
// must report what that run warned about, not what startup did.
func TestCapture_StartDropsTheEarlierWindow(t *testing.T) {
	t.Parallel()

	c := &a10rlog.Capture{}
	logger, _ := captureLogger(c)

	c.Start()
	logger.Warn("first")
	c.Stop()

	c.Start()
	logger.Warn("second")
	c.Stop()

	require.Equal(t, []string{"second"}, c.Messages())
}

// The WithAttrs / WithGroup path is how every slog.With logger reaches
// the handler, so a wrapper that loses itself there would silently
// capture nothing from the packages that pre-bind fields.
func TestCapture_SurvivesWithAttrs(t *testing.T) {
	t.Parallel()

	c := &a10rlog.Capture{}
	logger, _ := captureLogger(c)

	c.Start()
	logger.With(slog.String("component", "theme")).Warn("shadowed skin")
	c.Stop()

	require.Equal(t, []string{"shadowed skin"}, c.Messages())
}

// `--quiet` raises the file's level above Warn. The page exists to
// answer "what went wrong at start", so the window must keep
// collecting even when nothing reaches the file.
func TestCapture_CollectsAboveTheSinkLevel(t *testing.T) {
	t.Parallel()

	c := &a10rlog.Capture{}
	var sink bytes.Buffer
	inner := slog.NewTextHandler(&sink, &slog.HandlerOptions{Level: slog.LevelError})
	logger := slog.New(c.Wrap(inner))

	c.Start()
	logger.Warn("unknown skin")
	c.Stop()

	require.Equal(t, []string{"unknown skin"}, c.Messages())
	require.NotContains(t, sink.String(), "unknown skin")
}
