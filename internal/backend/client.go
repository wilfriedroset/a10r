// SPDX-License-Identifier: Apache-2.0

package backend

import (
	"context"

	"github.com/wilfriedroset/a10r/internal/config"
)

// Reader is the read-only subset of Client. Page models that only
// render data (alerts list, silences list, receivers list, status
// pane) accept *Reader* rather than the full Client so test fakes
// implement six methods, not fourteen.
//
// Every method takes a context.Context. Cancellation propagates to
// the underlying http.Request so a slow-responding backend does not
// stall the polling loop.
type Reader interface {
	ListAlerts(ctx context.Context, filter AlertFilter) ([]Alert, error)
	ListAlertGroups(ctx context.Context, filter AlertFilter) ([]AlertGroup, error)
	ListSilences(ctx context.Context, filter SilenceFilter) ([]Silence, error)
	GetSilence(ctx context.Context, id string) (Silence, error)
	ListReceivers(ctx context.Context) ([]Receiver, error)
	Status(ctx context.Context) (Status, error)
}

// Writer is the silence-mutation subset. The silence form (#30)
// accepts *Writer*; bulk silence and bulk expire on the alerts /
// silences pages do too.
//
// All write methods are non-idempotent: replaying a CreateSilence is
// a duplicate silence, replaying an ExpireSilence after a re-create
// expires the wrong silence. The C1 backoff loop must NOT auto-retry
// these on transient failures — the page UX prompts the user to
// confirm a retry instead.
type Writer interface {
	CreateSilence(ctx context.Context, spec SilenceSpec) (id string, err error)
	UpdateSilence(ctx context.Context, id string, spec SilenceSpec) error
	ExpireSilence(ctx context.Context, id string) error
}

// Client is the unified surface every backend implementation
// satisfies: vanilla Alertmanager v2, Mimir's prefixed v2 (with
// optional tenant header), and the multi-tenant fan-out layer.
//
// Per audit §5.1 there is one constructor for both backends — the
// Mimir wrapper composes a vanilla client with a prefix and tenant
// header — so the interface deliberately does NOT expose backend
// type. Capability-gated methods (config API, tenant admin, ring)
// return ErrUnsupported on backends that do not enable them; callers
// branch on Capabilities() before offering the action in the menu
// rather than relying on the returned error to drive UX.
type Client interface {
	Reader
	Writer

	// Capability-gated. Implementations return ErrUnsupported when
	// the corresponding Caps flag is false. v0.1 ships every backend
	// with these flags off; the methods exist so the post-v0.1 Mimir
	// config editor can land additively.

	GetConfig(ctx context.Context) (MimirConfig, error)
	SetConfig(ctx context.Context, cfg MimirConfig) error
	DeleteConfig(ctx context.Context) error
	ListTenantConfigs(ctx context.Context) ([]TenantConfig, error)
	RingStatus(ctx context.Context) (Ring, error)

	// Capabilities returns the runtime caps the implementation was
	// constructed with. The TUI uses this to filter menu actions
	// before any RPC is attempted.
	Capabilities() Caps
}

// Caps is the runtime view of `Capabilities` from `a10r.yaml`.
// Defined as a type alias so a rename or a new flag in the config
// schema surfaces here at compile time — keeps the two views in
// lockstep without a converter that could drift.
type Caps = config.Capabilities
