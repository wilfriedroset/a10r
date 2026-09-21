// SPDX-License-Identifier: Apache-2.0

// Package uistate persists the handful of view choices a10r reopens
// on: the tenant scope and each list page's sort column. One file,
// `<state-dir>/ui-state.yaml`, sitting next to the prompt-history
// files.
//
// The store is deliberately forgetful. A value equal to the built-in
// default is removed rather than written, and a state with nothing
// left to remember removes the file, so what remains on disk is
// short enough to read and edit by hand.
//
// Nothing here is load-bearing: every failure degrades to "no
// memory" with one log line. A user losing their remembered sort
// column is a smaller harm than a10r refusing to start because a
// state dir went read-only.
package uistate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/xdg"
)

// FileName is the state file's name inside the state dir.
const FileName = "ui-state.yaml"

// scopeAll is the "every configured backend" sentinel, matching the
// TUI's own scope vocabulary. It is the absence of a remembered
// scope, which is why it is never written.
const scopeAll = "all"

// State is the on-disk document. Sort maps a page resource name
// (`alerts`, `silences`, …) to the sorter's `<column key>:<asc|desc>`
// wire format; the sorter owns that format because it owns the
// column keys.
type State struct {
	Scope string            `yaml:"scope,omitempty"`
	Sort  map[string]string `yaml:"sort,omitempty"`
}

// Store is the read/write handle over one state file. Safe for
// concurrent use: setters mutate under a mutex and hand the write
// off to a single background goroutine, so nothing on the bubbletea
// update loop ever blocks on the filesystem.
type Store struct {
	path string

	mu     sync.Mutex
	state  State
	closed bool

	// sig carries "state changed" to the writer. Capacity 1 plus a
	// non-blocking send is the whole coalescing story: a change
	// landing while a write is in flight replaces the pending one
	// instead of queueing behind it. Nil marks a disabled store.
	sig  chan struct{}
	done chan struct{}

	// disabled is written only by the writer goroutine, after the
	// first failed write. Reads keep working; writes stop for the
	// rest of the process.
	disabled bool
}

// Open loads `<dir>/ui-state.yaml` and returns a usable Store. An
// empty dir disables the store entirely: reads answer zero values
// and writes are dropped, which is how an unresolvable state dir
// (or `tui.remember: false`) turns the feature off. A missing file
// is the first-run case. A malformed file logs once at warn and
// disables the store for the rest of the run, so the file is never
// deleted or rewritten: a hand-edited file with a typo is worth more
// to its author than a clean slate, and an empty state would
// otherwise flush over it on the first keypress.
func Open(dir string) *Store {
	if dir == "" {
		return &Store{}
	}
	path := filepath.Join(dir, FileName)
	st, ok := load(path)
	if !ok {
		return &Store{}
	}
	s := &Store{
		path:  path,
		state: st,
		sig:   make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
	go s.run()
	return s
}

// load reads the state file. The bool is false only when the file
// exists but cannot be parsed, which the caller turns into a
// read-only run.
func load(path string) (State, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("cannot read ui state",
				slog.String("path", path),
				slog.Any("err", err),
			)
		}
		return State{}, true
	}
	var st State
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&st); err != nil {
		slog.Warn("ignoring malformed ui state, memory is off for this run",
			slog.String("path", path),
			slog.Any("err", err),
		)
		return State{}, false
	}
	return st, true
}

// Scope returns the remembered tenant scope, or empty when nothing
// is remembered. Callers run it through PruneScope before trusting
// it against the current config.
func (s *Store) Scope() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Scope
}

// SetScope remembers scope. "all" and the empty string forget the
// key instead, because booting on every backend is the built-in
// default and needs no file entry.
func (s *Store) SetScope(scope string) {
	if scope == scopeAll {
		scope = ""
	}
	s.mutate(func(st *State) { st.Scope = scope })
}

// Sort returns the remembered `<key>:<asc|desc>` value for resource,
// or empty.
func (s *Store) Sort(resource string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Sort[resource]
}

// SetSort remembers value for resource. An empty value forgets the
// entry, which is how a page back on its default sort drops out of
// the file.
func (s *Store) SetSort(resource, value string) {
	s.mutate(func(st *State) {
		if value == "" {
			delete(st.Sort, resource)
			return
		}
		if st.Sort == nil {
			st.Sort = map[string]string{}
		}
		st.Sort[resource] = value
	})
}

// PruneSort forgets the sort entries whose key is not in known.
// Callers run it right after Open, where the process knows both the
// file and the current page set; a save knows only the value it
// writes. Nothing is written here on purpose: the next real change
// flushes the pruned map, and an entry nobody edits is not worth a
// write on every start.
func (s *Store) PruneSort(known []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for resource := range s.state.Sort {
		if !slices.Contains(known, resource) {
			delete(s.state.Sort, resource)
		}
	}
}

// Close flushes the pending write and stops the writer. Safe to
// call more than once; only the first call waits for the flush.
// Setters after Close leave the in-memory state alone rather than
// panicking on a closed channel.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.sig == nil || s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.sig)
	s.mu.Unlock()
	<-s.done
	return nil
}

func (s *Store) mutate(f func(*State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sig == nil || s.closed {
		return
	}
	before := State{Scope: s.state.Scope, Sort: maps.Clone(s.state.Sort)}
	f(&s.state)
	if s.state.Scope == before.Scope && maps.Equal(s.state.Sort, before.Sort) {
		// Re-pressing `-` on an already-default sort, or `0` on an
		// already-global scope, would otherwise rename the file for
		// no change.
		return
	}
	select {
	case s.sig <- struct{}{}:
	default:
	}
}

// run drains the coalescing signal until Close shuts it. Ranging a
// closed channel still yields the buffered value, so the last
// change before Close is written before the goroutine exits.
func (s *Store) run() {
	defer close(s.done)
	for range s.sig {
		s.flush()
	}
}

func (s *Store) flush() {
	if s.disabled {
		return
	}
	s.mu.Lock()
	snap := State{Scope: s.state.Scope, Sort: maps.Clone(s.state.Sort)}
	s.mu.Unlock()

	if err := s.write(snap); err != nil {
		slog.Debug("ui state not writable, memory disabled for this run",
			slog.String("path", s.path),
			slog.Any("err", err),
		)
		s.disabled = true
	}
}

func (s *Store) write(snap State) error {
	if snap.Scope == "" && len(snap.Sort) == 0 {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err //nolint:wrapcheck // the only caller logs the path alongside it.
		}
		return nil
	}
	b, err := yaml.Marshal(snap)
	if err != nil {
		return err //nolint:wrapcheck // the only caller logs the path alongside it.
	}
	if err := xdg.AtomicWrite(s.path, b); err != nil {
		return fmt.Errorf("write ui state: %w", err)
	}
	return nil
}

// PruneScope drops names that are not in known, preserving the
// order of the ones that survive, and returns the dropped names
// alongside so the caller can name them in a warning. "all", the
// empty string, and a scope whose every name has disappeared from
// the config all return "all" — the safe fan-out that shows the
// user everything they still have.
func PruneScope(scope string, known []string) (pruned string, dropped []string) {
	if scope == "" || scope == scopeAll {
		return scopeAll, nil
	}
	kept := make([]string, 0, len(known))
	for name := range strings.SplitSeq(scope, ",") {
		name = strings.TrimSpace(name)
		if slices.Contains(known, name) {
			kept = append(kept, name)
			continue
		}
		dropped = append(dropped, name)
	}
	if len(kept) == 0 {
		return scopeAll, dropped
	}
	return strings.Join(kept, ","), dropped
}
