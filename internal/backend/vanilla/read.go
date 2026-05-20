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

// fetchList runs the urlFor → doGet → decode → convert flow shared by
// every list endpoint. errCtx wraps any transport or decode failure
// so the caller gets a consistent prefix without re-implementing it
// in every method.
func fetchList[W, D any](ctx context.Context, c *Client, u, errCtx string, convert func(W) D) ([]D, error) {
	var raw []W
	if err := c.doGet(ctx, u, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", errCtx, err)
	}
	out := make([]D, 0, len(raw))
	for _, w := range raw {
		out = append(out, convert(w))
	}
	return out, nil
}

// fetchOne is the single-resource variant of fetchList for endpoints
// that return one decoded object instead of a list.
func fetchOne[W, D any](ctx context.Context, c *Client, u, errCtx string, convert func(W) D) (D, error) {
	var raw W
	var zero D
	if err := c.doGet(ctx, u, &raw); err != nil {
		return zero, fmt.Errorf("%s: %w", errCtx, err)
	}
	return convert(raw), nil
}

// ListAlerts implements backend.Reader.
func (c *Client) ListAlerts(ctx context.Context, filter backend.AlertFilter) ([]backend.Alert, error) {
	return fetchList(ctx, c, c.urlFor("/alerts", encodeAlertFilter(filter)), "list alerts", toAlert)
}

// ListAlertGroups implements backend.Reader.
func (c *Client) ListAlertGroups(ctx context.Context, filter backend.AlertFilter) ([]backend.AlertGroup, error) {
	return fetchList(ctx, c, c.urlFor("/alerts/groups", encodeAlertFilter(filter)), "list alert groups", toAlertGroup)
}

// ListSilences implements backend.Reader.
func (c *Client) ListSilences(ctx context.Context, filter backend.SilenceFilter) ([]backend.Silence, error) {
	return fetchList(ctx, c, c.urlFor("/silences", encodeSilenceFilter(filter)), "list silences", toSilence)
}

// GetSilence implements backend.Reader.
func (c *Client) GetSilence(ctx context.Context, id string) (backend.Silence, error) {
	if id == "" {
		return backend.Silence{}, errors.New("get silence: id is required")
	}
	return fetchOne(ctx, c, c.urlFor("/silence/"+url.PathEscape(id), nil), fmt.Sprintf("get silence %q", id), toSilence)
}

// ListReceivers implements backend.Reader.
func (c *Client) ListReceivers(ctx context.Context) ([]backend.Receiver, error) {
	return fetchList(ctx, c, c.urlFor("/receivers", nil), "list receivers", toReceiver)
}

// Status implements backend.Reader. The uptime field on the wire is
// the AM-startup timestamp; the backend.Status surface carries a
// duration, so we compute time.Since here.
func (c *Client) Status(ctx context.Context) (backend.Status, error) {
	return fetchOne(ctx, c, c.urlFor("/status", nil), "status", func(w wireStatus) backend.Status {
		return toStatus(w, time.Now)
	})
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

// ProbeReadyAt implements backend.Prober. Issues GET against
// /api/v2/status and returns the parsed `Date` response header as
// the server's view of "now". The doctor clock-skew check compares
// the returned timestamp against the local clock.
//
// Status (rather than /-/ready) is the target because /-/ready is
// often configured behind a load-balancer that strips response
// headers, while /api/v2/status traverses the full Alertmanager
// stack. Either would work in principle; status is the safer pick.
//
// A missing or unparseable Date header returns ErrNoDateHeader
// (the connection succeeded, the server simply did not advertise
// the timestamp). Other transport / 4xx / 5xx failures surface
// through the same wrapped contract as the other reads.
func (c *Client) ProbeReadyAt(ctx context.Context) (time.Time, error) {
	endpoint := c.urlFor("/status", nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return time.Time{}, fmt.Errorf("probe ready at: build request: %w", err)
	}
	hdr, err := c.execHeaders(req)
	if err != nil {
		return time.Time{}, fmt.Errorf("probe ready at: %w", err)
	}
	raw := hdr.Get("Date")
	if raw == "" {
		return time.Time{}, fmt.Errorf("probe ready at: %w", backend.ErrNoDateHeader)
	}
	t, err := http.ParseTime(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("probe ready at: %w: %w", backend.ErrNoDateHeader, err)
	}
	return t, nil
}
