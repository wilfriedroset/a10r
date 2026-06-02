// SPDX-License-Identifier: Apache-2.0

package output

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// WriteYAML marshals payload to w as YAML using gopkg.in/yaml.v3
// (already a project dep via internal/config). Indent is set to 2
// to match the file-side a10r.yaml convention, so a user who
// `a10r alerts list --output=yaml > snippet.yaml`-ed the output
// can paste fragments back into config without reformatting.
//
// The named return + deferred Close captures any flush error from
// yaml.Encoder.Close so a partial write (encode succeeded but the
// underlying writer flushed only part of the buffer) surfaces as
// a returned error rather than silently dropping the document
// tail. When both Encode and Close fail, the encode error wins —
// it carries the meaningful diagnostic; the close error is dropped.
func WriteYAML(w io.Writer, payload any) (err error) {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer func() {
		if cerr := enc.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close yaml encoder: %w", cerr)
		}
	}()
	if err = enc.Encode(payload); err != nil {
		return fmt.Errorf("encode yaml: %w", err)
	}
	return nil
}
