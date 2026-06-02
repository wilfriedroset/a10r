// SPDX-License-Identifier: Apache-2.0

package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// WriteJSON marshals payload to w as indented JSON terminated by a
// newline. The two-space indent matches jq's default and keeps
// diffs human-readable; the trailing newline ensures pipes to
// `tail`, `grep`, etc. don't lose the last record.
//
// SetEscapeHTML(false) is intentional: the payload is operator
// data (alert names, label values, URLs), not HTML, and the
// default `<`-/`>`/`&` escaping confuses jq users who copy a
// value out of the log.
func WriteJSON(w io.Writer, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}
