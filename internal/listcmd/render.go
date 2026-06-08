// SPDX-License-Identifier: Apache-2.0

package listcmd

import (
	"fmt"
	"io"

	"github.com/wilfriedroset/a10r/internal/output"
)

// JSONRenderer serialises rows as JSON. Each command's row type carries its own
// JSON tags, so there is no per-resource logic and one generic body serves all
// four list commands. A nil row slice is normalised to an empty slice so an
// empty result encodes as `[]` rather than `null`, keeping `... | jq '.[]'`
// a clean no-op instead of a type error.
func JSONRenderer[R any](w io.Writer, rows []R) error {
	if rows == nil {
		rows = []R{}
	}
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
