// SPDX-License-Identifier: Apache-2.0

package backendtest

import (
	"context"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// ClientStub satisfies backend.Client with backend.ErrUnsupported on
// every method. Embed it in a test fake and shadow only the methods
// the test actually exercises so an unintended call surfaces as a
// loud error rather than a silent zero value.
type ClientStub struct{}

var _ backend.Client = ClientStub{}

func (ClientStub) ListAlerts(context.Context, backend.AlertFilter) ([]backend.Alert, error) {
	return nil, backend.ErrUnsupported
}

func (ClientStub) ListSilences(context.Context, backend.SilenceFilter) ([]backend.Silence, error) {
	return nil, backend.ErrUnsupported
}

func (ClientStub) GetSilence(context.Context, string) (backend.Silence, error) {
	return backend.Silence{}, backend.ErrUnsupported
}

func (ClientStub) ListReceivers(context.Context) ([]backend.Receiver, error) {
	return nil, backend.ErrUnsupported
}

func (ClientStub) Status(context.Context) (backend.Status, error) {
	return backend.Status{}, backend.ErrUnsupported
}

func (ClientStub) CreateSilence(context.Context, backend.SilenceSpec) (string, error) {
	return "", backend.ErrUnsupported
}

func (ClientStub) UpdateSilence(context.Context, string, backend.SilenceSpec) error {
	return backend.ErrUnsupported
}

func (ClientStub) ExpireSilence(context.Context, string) error {
	return backend.ErrUnsupported
}

func (ClientStub) GetConfig(context.Context) (backend.MimirConfig, error) {
	return backend.MimirConfig{}, backend.ErrUnsupported
}

func (ClientStub) SetConfig(context.Context, backend.MimirConfig) error {
	return backend.ErrUnsupported
}

func (ClientStub) DeleteConfig(context.Context) error { return backend.ErrUnsupported }

func (ClientStub) ListTenantConfigs(context.Context) ([]backend.TenantConfig, error) {
	return nil, backend.ErrUnsupported
}

func (ClientStub) RingStatus(context.Context) (backend.Ring, error) {
	return backend.Ring{}, backend.ErrUnsupported
}

func (ClientStub) Capabilities() backend.Caps { return backend.Caps{} }
