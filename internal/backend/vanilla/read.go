// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// ListAlerts implements backend.Reader.
func (c *Client) ListAlerts(ctx context.Context, filter backend.AlertFilter) ([]backend.Alert, error) {
	u := c.urlFor("/alerts", encodeAlertFilter(filter))
	var raw []wireAlert
	if err := c.doGet(ctx, u, &raw); err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	out := make([]backend.Alert, 0, len(raw))
	for _, w := range raw {
		out = append(out, toAlert(w))
	}
	return out, nil
}

// ListAlertGroups implements backend.Reader.
func (c *Client) ListAlertGroups(ctx context.Context, filter backend.AlertFilter) ([]backend.AlertGroup, error) {
	u := c.urlFor("/alerts/groups", encodeAlertFilter(filter))
	var raw []wireAlertGroup
	if err := c.doGet(ctx, u, &raw); err != nil {
		return nil, fmt.Errorf("list alert groups: %w", err)
	}
	out := make([]backend.AlertGroup, 0, len(raw))
	for _, w := range raw {
		out = append(out, toAlertGroup(w))
	}
	return out, nil
}

// ListSilences implements backend.Reader.
func (c *Client) ListSilences(ctx context.Context, filter backend.SilenceFilter) ([]backend.Silence, error) {
	u := c.urlFor("/silences", encodeSilenceFilter(filter))
	var raw []wireSilence
	if err := c.doGet(ctx, u, &raw); err != nil {
		return nil, fmt.Errorf("list silences: %w", err)
	}
	out := make([]backend.Silence, 0, len(raw))
	for _, w := range raw {
		out = append(out, toSilence(w))
	}
	return out, nil
}

// GetSilence implements backend.Reader.
func (c *Client) GetSilence(ctx context.Context, id string) (backend.Silence, error) {
	if id == "" {
		return backend.Silence{}, errors.New("get silence: id is required")
	}
	u := c.urlFor("/silence/"+url.PathEscape(id), nil)
	var raw wireSilence
	if err := c.doGet(ctx, u, &raw); err != nil {
		return backend.Silence{}, fmt.Errorf("get silence %q: %w", id, err)
	}
	return toSilence(raw), nil
}

// ListReceivers implements backend.Reader.
func (c *Client) ListReceivers(ctx context.Context) ([]backend.Receiver, error) {
	u := c.urlFor("/receivers", nil)
	var raw []wireReceiver
	if err := c.doGet(ctx, u, &raw); err != nil {
		return nil, fmt.Errorf("list receivers: %w", err)
	}
	out := make([]backend.Receiver, 0, len(raw))
	for _, w := range raw {
		out = append(out, toReceiver(w))
	}
	return out, nil
}

// Status implements backend.Reader. The uptime field on the wire is
// the AM-startup timestamp; the backend.Status surface carries a
// duration, so we compute time.Since here.
func (c *Client) Status(ctx context.Context) (backend.Status, error) {
	u := c.urlFor("/status", nil)
	var raw wireStatus
	if err := c.doGet(ctx, u, &raw); err != nil {
		return backend.Status{}, fmt.Errorf("status: %w", err)
	}
	return toStatus(raw, time.Now), nil
}

// ProbeReady implements backend.Prober. Targets `/-/ready` —
// outside the /api/v2 prefix, so urlFor isn't reused here. A 2xx
// response returns nil; everything else surfaces as a wrapped
// transport-or-classified error matching exec's contract (most
// commonly ErrUnreachable for a transport failure or
// ErrUnauthorized for 401/403).
//
// Used by `a10r doctor` for the reachability check; not on the
// poll path so the per-request overhead does not matter.
func (c *Client) ProbeReady(ctx context.Context) error {
	endpoint := c.base + "/-/ready"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("probe ready: build request: %w", err)
	}
	if err := c.exec(req, nil); err != nil {
		return fmt.Errorf("probe ready: %w", err)
	}
	return nil
}
