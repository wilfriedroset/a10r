// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/wilfriedroset/a10r/internal/xdg"
)

// HistoryClass tags one of the per-prompt history rings. The label
// doubles as the on-disk file name so a class rename means a state
// migration — keep changes deliberate. Three classes keep `:` and
// `/` from cross-pollinating, and isolate the silences page's
// matcher-flavoured filter from generic substring filters on every
// other page (the latter would surface stale `creator=…`-shaped
// entries inside the alerts page's `/` prompt and vice-versa).
type HistoryClass string

const (
	// HistoryCmd backs the `:` command bar. The alias resolver
	// shares this ring so the user can recall a prior `:silences`
	// without retyping.
	HistoryCmd HistoryClass = "cmd-history"
	// HistoryFilter backs the `/` filter on every page where the
	// matcher is the four-mode lfk classifier (substring, fuzzy,
	// regex, literal — see searchmode.go).
	HistoryFilter HistoryClass = "filter-history"
	// HistorySilenceMatcher backs the silences page's `/` prompt,
	// where the matcher operates over Prom-style fields (creator,
	// comment, label-matchers) and the entries the user types look
	// nothing like the alerts-page filter ring's contents.
	HistorySilenceMatcher HistoryClass = "silence-matcher-history"
)

// historyMaxEntries is the per-class cap. 100 is the usual recent-
// command-stack feel — large enough that a typical session never
// rolls past, small enough that the file stays kilobyte-sized and a
// re-read on Open is effectively free. Not exposed as a knob: a
// configurable cap would force a yaml schema bump for a value users
// never reach for in practice.
const historyMaxEntries = 100

// HistoryFileMode is the permission stamped on each history file.
// See xdg.FileMode.
const HistoryFileMode = xdg.FileMode

// History is a bounded ring of recent prompt submissions for one
// matcher class, with a cursor for browsing previous entries. The
// public surface is the minimum the prompt needs:
//
//   - Append commits a fresh submission and persists.
//   - Prev / Next walk the ring relative to the cursor; Prev's
//     `draft` argument is the buffer the user had typed before they
//     started cycling, stashed on the first Prev so cycle-past-
//     newest in Next can restore it.
//   - Reset clears the cycle state so a fresh prompt session starts
//     uncycled. The prompt calls Reset on Open / commit / Esc.
//
// The struct is single-goroutine-safe by construction (bubbletea
// routes every Update through one goroutine), so no mutex.
type History struct {
	path    string
	entries []string
	// cursor is -1 when not cycling. Otherwise it indexes into
	// entries from the tail end: cursor=0 is the newest entry,
	// cursor=len(entries)-1 is the oldest. This matches the shell
	// convention (Up walks from newest to oldest).
	cursor int
	// draft holds the buffer the user had typed before they started
	// cycling so Reset / cycle-past-newest can restore it. Empty
	// when no cycle session is active.
	draft string
}

// NewHistory loads the on-disk ring for the given class. A missing
// file, an unreadable file, or a malformed line all degrade to an
// empty in-memory ring rather than surfacing the error — the prompt
// stays usable and the next Append rewrites the file with valid
// content. Callers that need to know about disk failures should log
// inline; the prompt does not.
//
// dir is the resolved state directory (typically returned by
// HistoryDir). Pass an empty string to disable persistence — Append
// becomes in-memory only, useful for tests.
func NewHistory(dir string, class HistoryClass) *History {
	h := &History{cursor: -1}
	if dir == "" {
		return h
	}
	h.path = filepath.Join(dir, string(class))
	h.entries = readHistoryFile(h.path)
	return h
}

// HistoryDir resolves the state directory for the history rings.
// Thin alias over xdg.StateDir, kept as the footer package's own
// seam so callers (boot, the prompt) do not reach across.
//
// env and homeDir are injected so the test suite can drive every
// branch from a single host without setenv contamination.
func HistoryDir(env func(string) string, homeDir func() (string, error)) (string, error) {
	return xdg.StateDir(env, homeDir) //nolint:wrapcheck // pass-through alias: xdg already wraps with "user home: ".
}

// DefaultHistoryDir is the production path resolver. Callers that
// want dependency injection (the wizard, tests) should call
// HistoryDir directly.
func DefaultHistoryDir() (string, error) {
	return xdg.DefaultStateDir() //nolint:wrapcheck // xdg already wraps; the caller reports the path verbatim.
}

// Len returns the entry count.
func (h *History) Len() int {
	if h == nil {
		return 0
	}
	return len(h.entries)
}

// Path returns the on-disk path the ring would persist to, or an
// empty string when persistence is disabled. Test-only seam.
func (h *History) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

// Append commits a submission to the ring. Empty input and an input
// equal to the most-recent entry are dropped — the dedup is
// adjacent-only so non-adjacent duplicates remain (a user who runs
// `:alerts` then `:silences` then `:alerts` should see all three on
// recall, not the second one collapsed). Persistence is best-effort:
// a write failure leaves the in-memory ring authoritative for the
// rest of the session.
func (h *History) Append(value string) {
	if h == nil || value == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == value {
		// Adjacent dedup: still resets the cycle session so a
		// post-submit re-open doesn't resume mid-walk.
		h.Reset()
		return
	}
	h.entries = append(h.entries, value)
	if over := len(h.entries) - historyMaxEntries; over > 0 {
		// Drop the oldest `over` entries. Slice-and-copy rather
		// than a tail re-slice so we don't pin the original
		// backing array — the ring lives for the process lifetime
		// and the GC should be free to reclaim the dropped prefix.
		h.entries = append([]string(nil), h.entries[over:]...)
	}
	h.Reset()
	if h.path != "" {
		if err := writeHistoryFile(h.path, h.entries); err != nil {
			// Best-effort: the in-memory ring stays authoritative
			// for the rest of the session, and the next Append
			// will retry the write. Log once at Warn so an
			// operator debugging "history isn't persisting" can
			// find the actual fs error in a10r.log instead of
			// guessing at permissions.
			slog.Warn("history persist failed",
				slog.String("path", h.path),
				slog.Any("err", err),
			)
		}
	}
}

// Prev walks one step toward older entries. Returns the entry at
// the new cursor and true on success, or ("", false) when the ring
// is empty or the cursor is already at the oldest entry. The first
// Prev call after Reset stashes the supplied draft so a later
// cycle-past-newest can restore it.
func (h *History) Prev(draft string) (string, bool) {
	if h == nil || len(h.entries) == 0 {
		return "", false
	}
	switch {
	case h.cursor == -1:
		h.draft = draft
		h.cursor = 0
	case h.cursor < len(h.entries)-1:
		h.cursor++
	default:
		// Already on the oldest entry — bail rather than wrap so
		// the user has a stable bottom of the stack.
		return "", false
	}
	return h.entries[len(h.entries)-1-h.cursor], true
}

// Next walks one step toward newer entries. Returns the entry at
// the new cursor and true. When the cursor crosses the newest
// entry, returns the stashed draft (the buffer from before the
// cycle started) and resets — that's what "reaching present clears
// the buffer back to whatever the user typed" means.
func (h *History) Next() (string, bool) {
	if h == nil || h.cursor == -1 {
		return "", false
	}
	if h.cursor == 0 {
		draft := h.draft
		h.Reset()
		return draft, true
	}
	h.cursor--
	return h.entries[len(h.entries)-1-h.cursor], true
}

// Reset clears the cycle state. Called by Open/Close on the prompt
// and after Append so a fresh prompt session starts uncycled.
func (h *History) Reset() {
	if h == nil {
		return
	}
	h.cursor = -1
	h.draft = ""
}

// Cycling reports whether the user is currently mid-walk. The
// prompt uses this to decide whether to broadcast PromptChangedMsg
// when the buffer mutates from a Prev/Next swap (it should — pages
// recompute the live filter).
func (h *History) Cycling() bool {
	if h == nil {
		return false
	}
	return h.cursor != -1
}

// readHistoryFile loads a ring file. Returns nil on any error. The
// caller treats nil and empty identically — both mean "no history",
// and the next Append will (re)create the file. Trims surrounding
// whitespace and drops empty lines so a hand-edited file with a
// trailing newline (the editor convention) doesn't leave a phantom
// blank entry.
func readHistoryFile(path string) []string {
	f, err := os.Open(path) //#nosec G304 -- path comes from a trusted XDG resolver, not user input
	if err != nil {
		// Missing file is the common case on first run; not an
		// error worth raising. Permission / corruption errors all
		// route to "no history" — same shape, less surface for the
		// caller.
		return nil
	}
	defer func() { _ = f.Close() }()
	out := make([]string, 0, historyMaxEntries)
	scanner := bufio.NewScanner(f)
	// Bound the per-line buffer at 1 MiB so a malformed file with a
	// huge single line can't OOM the process; well above the
	// expected 200-byte ceiling for a realistic alias / filter.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		// Partial read — keep whatever we got, the next Append
		// will rewrite the file with the in-memory state. Silent
		// on purpose: the prompt has nowhere to surface a "history
		// got truncated" warning the user would care about.
		return out
	}
	if over := len(out) - historyMaxEntries; over > 0 {
		out = out[over:]
	}
	return out
}

// writeHistoryFile rewrites the ring file: one entry per line,
// trailing newline, which is what readHistoryFile's Scanner expects.
func writeHistoryFile(path string, entries []string) error {
	var buf bytes.Buffer
	for _, e := range entries {
		buf.WriteString(e)
		buf.WriteByte('\n')
	}
	return xdg.AtomicWrite(path, buf.Bytes()) //nolint:wrapcheck // xdg already wraps with a "state …" prefix; Append logs it verbatim.
}
