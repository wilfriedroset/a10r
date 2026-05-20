// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// CreateSilence implements backend.Writer. Returns the new silence
// id assigned by Alertmanager.
func (c *Client) CreateSilence(ctx context.Context, spec backend.SilenceSpec) (string, error) {
	return c.postSilence(ctx, "", spec)
}

// UpdateSilence implements backend.Writer. Alertmanager distinguishes
// create from update by whether the body's `id` field is set, so an
// empty id here is rejected locally rather than letting the server
// create a duplicate silence.
func (c *Client) UpdateSilence(ctx context.Context, id string, spec backend.SilenceSpec) error {
	if id == "" {
		return errors.New("update silence: id is required")
	}
	_, err := c.postSilence(ctx, id, spec)
	return err
}

func (c *Client) ExpireSilence(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("expire silence: id is required")
	}
	if err := c.doDelete(ctx, c.urlFor("/silence/"+url.PathEscape(id), nil)); err != nil {
		return fmt.Errorf("expire silence %q: %w", id, err)
	}
	return nil
}

// postSilence is the shared body of CreateSilence and UpdateSilence.
// id="" creates; id!="" updates.
func (c *Client) postSilence(ctx context.Context, id string, spec backend.SilenceSpec) (string, error) {
	body := wirePostableSilence{
		ID:        id,
		Matchers:  toWireMatchers(spec.Matchers),
		StartsAt:  spec.StartsAt,
		EndsAt:    spec.EndsAt,
		CreatedBy: spec.CreatedBy,
		Comment:   spec.Comment,
	}
	var resp wirePostSilenceResponse
	if err := c.doPost(ctx, c.urlFor("/silences", nil), body, &resp); err != nil {
		verb := "create silence"
		if id != "" {
			verb = "update silence"
		}
		return "", fmt.Errorf("%s: %w", verb, err)
	}
	return resp.SilenceID, nil
}
