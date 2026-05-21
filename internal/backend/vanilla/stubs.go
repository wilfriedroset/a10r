// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"context"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// Capability-gated method stubs. Vanilla Alertmanager has no Mimir-
// admin equivalents, so each method returns ErrUnsupported. The
// Mimir wrapper overrides these when its Caps allow; for the
// vanilla path, callers branch on Capabilities() before attempting.
// A future Mimir config editor (see ADR 0028) will replace these
// stubs in the Mimir package with real implementations; vanilla's
// stubs stay.

func (*Client) GetConfig(context.Context) (backend.MimirConfig, error) {
	return backend.MimirConfig{}, backend.ErrUnsupported
}

func (*Client) SetConfig(context.Context, backend.MimirConfig) error {
	return backend.ErrUnsupported
}

func (*Client) DeleteConfig(context.Context) error {
	return backend.ErrUnsupported
}

func (*Client) ListTenantConfigs(context.Context) ([]backend.TenantConfig, error) {
	return nil, backend.ErrUnsupported
}

func (*Client) RingStatus(context.Context) (backend.Ring, error) {
	return backend.Ring{}, backend.ErrUnsupported
}
