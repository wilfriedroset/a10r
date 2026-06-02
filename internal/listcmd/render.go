// SPDX-License-Identifier: Apache-2.0

package listcmd

import (
	"fmt"
	"io"

	"github.com/wilfriedroset/a10r/internal/output"
)

// JSONRenderer serialises rows as JSON. Each command's row type carries its own
// JSON tags, so there is no per-resource logic and one generic body serves all
// four list commands.
func JSONRenderer[R any](w io.Writer, rows []R) error {
	if err := output.WriteJSON(w, rows); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

func YAMLRenderer[R any](w io.Writer, rows []R) error {
	if err := output.WriteYAML(w, rows); err != nil {
		return fmt.Errorf("write yaml: %w", err)
	}
	return nil
}
