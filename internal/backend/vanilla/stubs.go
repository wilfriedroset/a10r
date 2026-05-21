// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"context"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// Capability-gated method stubs. Vanilla Alertmanager has no Mimir-
// admin equivalents, so each method returns ErrUnsupported. Callers
// branch on Capabilities() before attempting (see ADR 0028).

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
