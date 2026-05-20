// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"context"
	"errors"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// submitDoneMsg is the result of an async CreateSilence /
// UpdateSilence round-trip. Form.Update routes it back through
// Form.applySubmitDone or emits SubmittedMsg on the next tick.
// Kept private — the silence form is the only producer and
// consumer. gen pins the submit attempt this message belongs to;
// if the form was popped (Esc) and a fresh form pushed, the
// generation no longer matches and the message is discarded so it
// can't auto-pop the new form with stale content.
type submitDoneMsg struct {
	gen     int
	id      string
	updated bool
	err     error
}

// submitter owns the silence-form submit lifecycle: cancellable
// ctx wiring, the in-flight generation token, the re-entry guard,
// and the mutex that guards the cancel func against the
// Close()-vs-goroutine race. Form holds one by value and calls
// Start / Cancel / Done explicitly so the cancellation protocol
// can be exercised in isolation without booting a full Form.
type submitter struct {
	// parent is the parent ctx the Create/UpdateSilence call
	// derives from. Mirrors Options.SubmitCtx — cancelling cancels
	// the in-flight write so app-level shutdown propagates through
	// the ctx (not only through Close). Nil means "no parent
	// pinned"; Start falls back to context.Background() so single-
	// shot tests that don't care about app-level propagation stay
	// green.
	parent context.Context //nolint:containedctx // submit write ctx, plumbed once at construction.

	// inFlight is true between Start scheduling the write and
	// Done(msg) being called for that gen. While set, a second
	// Start is rejected (returns false) so a slow tenant cannot be
	// double-submitted by an impatient operator hammering Ctrl+S.
	inFlight bool

	// gen is bumped on every Start. The active write's goroutine
	// carries the value at the time it was queued; on arrival Done
	// discards the message if the generation no longer matches —
	// the operator pressed Esc and the form was popped (or
	// re-pushed) before this round-trip completed, so the result
	// must not be projected onto whatever form is on top now.
	gen int

	// cancel cancels the context handed to the in-flight Create /
	// UpdateSilence call so Cancel() (form pop / app shutdown)
	// aborts the request instead of letting the goroutine outlive
	// the form. Guarded by mu because the submit goroutine clears
	// it while Cancel() (Update goroutine) reads it. Nil when no
	// submit is in flight.
	mu     sync.Mutex
	cancel context.CancelFunc
}

// InFlight reports whether a submit is currently waiting on the
// backend. Used by Form to surface a flash hint and drop a
// reflex Ctrl+S re-entry without invoking the write goroutine.
func (s *submitter) InFlight() bool { return s.inFlight }

// Start schedules an async Create / Update against client and
// returns the tea.Cmd that runs it. id == "" picks CreateSilence;
// non-empty picks UpdateSilence(id, spec). Returns nil if a
// submit is already in flight — caller surfaces the rejection.
// Caller is responsible for spec validation; Start does no
// parsing.
func (s *submitter) Start(client Client, id string, spec backend.SilenceSpec) tea.Cmd {
	if s.inFlight {
		return nil
	}
	s.gen++
	gen := s.gen
	s.inFlight = true

	parent := s.parent
	if parent == nil {
		parent = context.Background()
	}

	s.mu.Lock()
	if s.cancel != nil {
		// A previous submit was somehow still in flight; cancel it
		// so we don't have two writes racing on the same form.
		s.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.mu.Unlock()

	clearCancel := func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		cancel()
	}

	if id != "" {
		return func() tea.Msg {
			defer clearCancel()
			err := client.UpdateSilence(ctx, id, spec)
			return submitDoneMsg{gen: gen, id: id, updated: true, err: err}
		}
	}
	return func() tea.Msg {
		defer clearCancel()
		newID, err := client.CreateSilence(ctx, spec)
		return submitDoneMsg{gen: gen, id: newID, err: err}
	}
}

// Cancel aborts the in-flight write (if any) by cancelling its
// derived ctx. Safe to call concurrently with the submit
// goroutine — mu serialises the cancel slot. No-op when nothing
// is in flight.
func (s *submitter) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Done routes a submitDoneMsg back into the submitter. Returns
// stale=true when the message belongs to a previous generation
// (the user hit Esc and a new submit may now be live) — caller
// drops the message. Otherwise the in-flight flag is cleared and
// the message is returned alongside stale=false so the caller can
// surface the result.
func (s *submitter) Done(msg submitDoneMsg) (stale bool) {
	if msg.gen != s.gen {
		return true
	}
	s.inFlight = false
	return false
}

// submitNow parses the buffers and routes to one of three shapes:
// bulk-create emits BulkSubmittedMsg (the page fans out per-target);
// edit calls UpdateSilence; create calls CreateSilence.
//
// Validation runs synchronously (cheap, no I/O). The HTTP round-trip
// runs inside the returned tea.Cmd so bubbletea executes it on its
// own goroutine — without this indirection, a slow tenant would
// freeze the Update loop for up to the transport timeout. Result is
// posted as submitDoneMsg and translated by applySubmitDone.
//
// Re-entry guard: a second Ctrl+S while a submit is already in
// flight flashes a hint and drops the new attempt. Without this an
// impatient operator on a slow tenant would post duplicate
// CreateSilence requests by reflex; the existing submit is going to
// land in the transport timeout anyway.
func (f *Form) submitNow() tea.Cmd {
	if f.submit.InFlight() {
		return flashFn("silence: submit already in flight")
	}
	spec, err := f.parseSpec()
	if err != nil {
		return f.fail(err.Error())
	}
	if f.bulk {
		// Clients may legitimately be nil/empty in bulk mode — the
		// page owns dispatch, the form just collects metadata.
		return func() tea.Msg {
			return BulkSubmittedMsg{
				Comment:  spec.Comment,
				StartsAt: spec.StartsAt,
				EndsAt:   spec.EndsAt,
				Creator:  spec.CreatedBy,
			}
		}
	}
	// Resolve the write target from the active tenant. Defensive:
	// in normal flow f.tenant is set by the caller (initial pick)
	// or by a PickerSubmittedMsg landing on the form, and the
	// resolved client is non-nil. An empty tenant or a missing key
	// is unreachable through the UI but worth refusing loudly so a
	// future refactor that loses the wiring fails closed.
	if f.tenant == "" {
		return f.fail("no tenant selected")
	}
	client, ok := f.clients[f.tenant]
	if !ok || client == nil {
		return f.fail("no client for tenant " + f.tenant)
	}
	return f.submit.Start(client, f.editID, spec)
}

// applySubmitDone routes a submitDoneMsg back into the form. Stale
// generations (the user hit Esc and a new form may now be live) are
// silently dropped — the message must not auto-pop a different
// form or flash a stale "silence created" on whatever page is on
// top. Errors flash + keep the form open; success emits the
// appropriate auto-pop message. Runs on the Update goroutine so
// f.err / f.fail mutations are race-free.
func (f *Form) applySubmitDone(m submitDoneMsg) tea.Cmd {
	if stale := f.submit.Done(m); stale {
		return nil
	}
	if m.err != nil {
		if errors.Is(m.err, context.Canceled) {
			// Cancellation is shutdown noise, not a backend failure.
			// The submit goroutine returned because Close() / SIGTERM
			// cancelled SubmitCtx — the user sees no misleading flash
			// and the page-pop has already auto-popped the form.
			return nil
		}
		return f.fail(m.err.Error())
	}
	id := m.id
	updated := m.updated
	return func() tea.Msg { return SubmittedMsg{ID: id, Updated: updated} }
}

// flashFn is a tiny helper for surfacing a single ephemeral hint
// without touching f.err — used when the form rejects a keystroke
// (e.g. duplicate Ctrl+S during an in-flight submit) without
// recording it as the persistent submit error.
func flashFn(text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: footer.FlashWarn, Text: text}
	}
}

// fail records the error on the form and returns a Cmd that
// surfaces it as a flash.
func (f *Form) fail(text string) tea.Cmd {
	f.err = text
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: footer.FlashError, Text: "silence: " + text}
	}
}
