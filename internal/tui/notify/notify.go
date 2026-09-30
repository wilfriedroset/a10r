// SPDX-License-Identifier: Apache-2.0

// Package notify raises a flash, a terminal bell and a desktop
// notification when a poll brings a firing alert the poll before it
// did not have. It hangs off the App rather than the alerts page so
// the signal still reaches a user who is reading another page.
package notify

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

const (
	title          = "a10r"
	labelAlertname = "alertname"
	bell           = "\a"
	// commandTimeout bounds the subprocess so a notifier that hangs
	// does not leak a process per poll.
	commandTimeout = time.Second
)

// Notifier holds the settings and the per-tenant firing set the diff
// runs against. It exists whether or not the feature is on, so a
// reload that switches it on has something to apply to.
//
// Only commandFailed is safe to touch off the event loop; seen and
// scope are mutated unguarded and belong to the Update goroutine.
type Notifier struct {
	enabled bool
	minRank int
	bell    bool
	desktop string
	command []string

	scope string
	// seen maps a tenant to the alertnames that were active at its
	// previous poll. A tenant with no entry is warming up. The rank
	// values ride along because activeRanks already computed them;
	// only membership is read back.
	seen map[string]map[string]int

	run Runner
	// commandFailed is atomic because two of these tea.Cmds can race
	// each other: one poll per tenant is in flight at a time, and each
	// runs the subprocess on its own goroutine.
	commandFailed atomic.Bool
}

// Runner is the subprocess seam. RunCommand is the production one.
type Runner func(ctx context.Context, name string, args ...string) error

// New returns a notifier under cfg. A disabled one announces nothing.
func New(cfg config.Notify) *Notifier {
	return NewWithRunner(cfg, RunCommand)
}

// NewWithRunner is New with the subprocess swapped, so a caller
// outside this package can prove which runs spawn nothing.
func NewWithRunner(cfg config.Notify, run Runner) *Notifier {
	n := &Notifier{run: run, seen: map[string]map[string]int{}}
	n.Apply(cfg)
	return n
}

// Apply replaces the settings. Switching the feature on drops the
// firing sets an off notifier stopped updating, so it warms up rather
// than announce what was already firing. Staying on keeps them, or a
// reload would swallow the alert that started firing across it; the
// sets ignore the severity floor, so no other setting needs a reset.
func (n *Notifier) Apply(cfg config.Notify) {
	if cfg.Enabled && !n.enabled {
		n.seen = map[string]map[string]int{}
	}
	n.enabled = cfg.Enabled
	n.minRank = backend.SeverityRank(map[string]string{"severity": cfg.MinSeverityOrDefault()})
	n.bell = cfg.BellOrDefault()
	n.desktop = cfg.DesktopOrDefault()
	n.command = cfg.Command
}

// Observe diffs one tenant's alert snapshot against the previous one
// and returns the batch that announces the new firing alertnames. It
// returns nil when the feature is off, for a tenant outside the
// scope, for a tenant that is warming up, and when nothing new clears
// the severity floor.
func (n *Notifier) Observe(tenant string, alerts []backend.Alert) tea.Cmd {
	if !n.enabled || !config.ScopeMatches(n.scope, tenant) {
		return nil
	}
	current := activeRanks(alerts)
	previous, seeded := n.seen[tenant]
	// The full active set is stored, not only the part above the
	// floor, so a below-floor alert cannot announce itself later by
	// raising its severity.
	n.seen[tenant] = current
	if !seeded {
		return nil
	}
	names, worst := newFiring(previous, current, n.minRank)
	if len(names) == 0 {
		return nil
	}
	return n.emit(formatBody(tenant, names, worst))
}

// SetScope narrows or widens the tenants Observe answers to. A tenant
// the new scope excludes loses its set, so it warms up again if it
// comes back.
func (n *Notifier) SetScope(scope string) {
	n.scope = scope
	maps.DeleteFunc(n.seen, func(tenant string, _ map[string]int) bool {
		return !config.ScopeMatches(scope, tenant)
	})
}

// activeRanks reduces a snapshot to alertname -> worst severity rank,
// counting active instances only: a suppressed or unprocessed alert
// is not firing.
func activeRanks(alerts []backend.Alert) map[string]int {
	out := make(map[string]int, len(alerts))
	for _, a := range alerts {
		if a.State != backend.AlertStateActive {
			continue
		}
		name := a.Labels[labelAlertname]
		rank := backend.SeverityRank(a.Labels)
		if known, ok := out[name]; !ok || rank > known {
			out[name] = rank
		}
	}
	return out
}

// newFiring returns the alertnames that are active now, were not
// active before, and reach minRank, plus the worst rank among them.
// The names are sorted so a one-alert body is reproducible.
func newFiring(previous, current map[string]int, minRank int) (names []string, worst int) {
	for name, rank := range current {
		if _, known := previous[name]; known || rank < minRank {
			continue
		}
		names = append(names, name)
		worst = max(worst, rank)
	}
	slices.Sort(names)
	return names, worst
}

// formatBody sanitises at the source: the body reaches the flash
// strip, an OSC payload and a subprocess argument, and a name with a
// newline in it would break the one-line flash before it ever got
// near the escape sequence.
func formatBody(tenant string, names []string, worst int) string {
	tenant = sanitize(tenant)
	if len(names) == 1 {
		return tenant + ": " + sanitize(names[0]) + " (" + backend.SeverityLabel(worst) + ")"
	}
	return tenant + ": " + strconv.Itoa(len(names)) + " new firing alerts, worst " + backend.SeverityLabel(worst)
}

// emit fans the body out to the flash strip, the terminal and the
// subprocess. The flash is weak: a keystroke flash younger than
// footer.WeakFlashGuard suppresses it, even when every transport is off.
func (n *Notifier) emit(body string) tea.Cmd {
	cmds := []tea.Cmd{footer.ShowWeakFlash(footer.FlashWarn, body)}
	raw := desktopSequence(n.desktop, body)
	if n.bell {
		raw += bell
	}
	if raw != "" {
		// Through the program, never os.Stdout: the renderer owns the
		// terminal and a direct write would tear the frame.
		cmds = append(cmds, tea.Raw(raw))
	}
	if len(n.command) > 0 {
		cmds = append(cmds, n.commandCmd(body))
	}
	return tea.Batch(cmds...)
}

func desktopSequence(desktop, body string) string {
	// formatBody already sanitised this. Repeated because the sink
	// guards itself, so a future caller cannot bypass the guard.
	body = sanitize(body)
	osc777 := "\x1b]777;notify;" + title + ";" + body + bell
	osc9 := "\x1b]9;" + body + bell
	switch desktop {
	case config.NotifyDesktopOSC777:
		return osc777
	case config.NotifyDesktopOSC9:
		return osc9
	case config.NotifyDesktopBoth:
		return osc777 + osc9
	}
	return ""
}

// sanitize replaces every control character and every `;` with a
// space. A tenant name and an alertname both arrive from outside, so
// neither must be able to close an OSC sequence and inject its own,
// nor to turn the one-line flash into a block.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ';' {
			return ' '
		}
		return r
	}, s)
}

// substituteMessage replaces every argv element that is exactly the
// placeholder with body as one whole argument. There is no shell, so
// no splitting and no other expansion.
func substituteMessage(argv []string, body string) []string {
	out := make([]string, len(argv))
	for i, arg := range argv {
		if arg == config.NotifyMessagePlaceholder {
			out[i] = body
			continue
		}
		out[i] = arg
	}
	return out
}

// commandCmd runs the configured program off the event loop. A
// failure flashes once per session, so a missing notify-send does not
// flash on every poll.
func (n *Notifier) commandCmd(body string) tea.Cmd {
	argv := substituteMessage(n.command, body)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		err := n.run(ctx, argv[0], argv[1:]...)
		if err == nil || !n.commandFailed.CompareAndSwap(false, true) {
			return nil
		}
		return footer.FlashShowMsg{Level: footer.FlashWarn, Text: "notify command: " + err.Error(), Weak: true}
	}
}

// RunCommand discards stdout and stderr: the program is a notifier,
// and its output belongs on the user's desktop, not in the frame.
func RunCommand(ctx context.Context, name string, args ...string) error {
	// #nosec G204 -- the argv is the user's own config, and it runs
	// without a shell, so there is nothing to inject into.
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}
