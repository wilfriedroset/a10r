// SPDX-License-Identifier: Apache-2.0

package backend

import (
	"context"
	"time"

	"github.com/wilfriedroset/a10r/internal/config"
)

// Reader is the read-only subset of Client. Read-only page models accept
// Reader so test fakes implement five methods, not fourteen. Context
// cancellation propagates to the http.Request so a slow backend cannot
// stall the polling loop.
type Reader interface {
	ListAlerts(ctx context.Context, filter AlertFilter) ([]Alert, error)
	ListSilences(ctx context.Context, filter SilenceFilter) ([]Silence, error)
	GetSilence(ctx context.Context, id string) (Silence, error)
	ListReceivers(ctx context.Context) ([]Receiver, error)
	Status(ctx context.Context) (Status, error)
}

// Writer is the silence-mutation subset. All write methods are
// non-idempotent (replaying CreateSilence duplicates, replaying
// ExpireSilence after a re-create expires the wrong silence), so the C1
// backoff loop must NOT auto-retry these — the page UX prompts to confirm.
type Writer interface {
	CreateSilence(ctx context.Context, spec SilenceSpec) (id string, err error)
	UpdateSilence(ctx context.Context, id string, spec SilenceSpec) error
	ExpireSilence(ctx context.Context, id string) error
}

// Client is the unified surface every backend satisfies (vanilla v2, Mimir
// prefixed v2, and the multi-tenant fan-out). Per ADR 0028 one constructor
// serves both backends, so the interface deliberately does NOT expose
// backend type; callers branch on Capabilities() before offering an action
// rather than driving UX off the returned ErrUnsupported.
type Client interface {
	Reader
	Writer

	// Capability-gated: return ErrUnsupported when the Caps flag is false.
	GetConfig(ctx context.Context) (MimirConfig, error)
	SetConfig(ctx context.Context, cfg MimirConfig) error
	DeleteConfig(ctx context.Context) error
	ListTenantConfigs(ctx context.Context) ([]TenantConfig, error)
	RingStatus(ctx context.Context) (Ring, error)

	// Capabilities lets the TUI filter menu actions before any RPC.
	Capabilities() Caps
}

// Caps is the runtime view of config.Capabilities. A type alias (not a new
// type) so a config-schema rename or new flag surfaces here at compile time
// without a converter that could drift.
type Caps = config.Capabilities

// Prober is the small probe surface `a10r doctor` consumes (ADR 0039).
// Kept separate from Reader so existing Reader fakes need not grow methods.
type Prober interface {
	// ProbeReady issues GET /-/ready; non-2xx and transport errors surface
	// as wrapped ErrUnreachable, so callers must not assume a finer type.
	ProbeReady(ctx context.Context) error

	// ProbeReadyAt returns the GET /api/v2/status Date header as the
	// server's "now" for the clock-skew check (>30s drift warns). A missing
	// or unparseable header surfaces as ErrNoDateHeader so the caller
	// renders Skipped rather than Warning.
	ProbeReadyAt(ctx context.Context) (time.Time, error)

	// ProbeAlertmanagerMount GETs <BaseURL>/alertmanager/api/v2/status
	// ignoring the configured prefix; only a nil return licenses the doctor
	// AuthChecker to claim that adding `prefix: /alertmanager` is a verified
	// fix for a Status() 404.
	ProbeAlertmanagerMount(ctx context.Context) error
}
