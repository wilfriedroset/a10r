// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
)

// foundSilence is one backend's hit when resolving a silence id: the
// tenant it lives in and the silence as the backend returned it.
type foundSilence struct {
	tenant  string
	silence backend.Silence
}

// findSilences resolves a silence id across the in-scope backends for
// the write verbs (update / expire / recreate). It is lenient like the
// get verbs: a clean ErrNotFound on a backend is "absent here", not a
// failure, so an id living in one tenant does not make the others look
// broken. Every match is returned (an id can be mirrored across
// tenants). When nothing matches it returns the exit-coded error —
// ExitUnreachable if every backend failed (could not look),
// ExitNotFound otherwise (genuinely absent).
func findSilences(
	ctx context.Context,
	errOut io.Writer,
	cfg *config.Config,
	build listcmd.ClientFactory,
	id string,
) ([]foundSilence, error) {
	results := fanOutBackends(ctx, cfg, build,
		func(ctx context.Context, tenant string, c backend.Client) ([]foundSilence, error) {
			s, err := c.GetSilence(ctx, id)
			if errors.Is(err, backend.ErrNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, fmt.Errorf("get silence: %w", err)
			}
			return []foundSilence{{tenant: tenant, silence: s}}, nil
		})
	failed, _ := emitBackendErrors(errOut, results)

	var found []foundSilence
	for _, r := range results {
		found = append(found, r.value...)
	}
	if len(found) == 0 {
		// A miss while any backend was unreachable is "could not confirm"
		// (the id may live on the backend that failed): signal retry, not
		// a confident absence. Only an all-reachable miss is ExitNotFound.
		if failed > 0 {
			return nil, NewExitError(ExitUnreachable,
				fmt.Errorf("a backend in scope failed; silence %q not confirmed", id))
		}
		return nil, NewExitError(ExitNotFound,
			fmt.Errorf("silence %q not found in scope", id))
	}
	return found, nil
}
