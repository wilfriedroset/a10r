// SPDX-License-Identifier: Apache-2.0

package report

import (
	"io"

	"github.com/wilfriedroset/a10r/internal/config"
)

// ConfigInput is the resolved-configuration answer the report prints:
// which files a start read, in the order the merge applied them, and
// what that run warned about.
type ConfigInput struct {
	Sources  []config.Source
	Warnings []string
}

// SourcesHeader and WarningsHeader are the line prefixes the two
// sections start with. They are exported because the `:config` page
// anchors jump by prefix, and a reworded header would otherwise break
// those keys with nothing failing.
const (
	SourcesHeader  = "sources ("
	WarningsHeader = "warnings ("
)

// Config writes the human-readable sources-and-warnings report to
// out. Format is pinned by
// internal/report/testdata/config_*.golden.
//
// Both sections always print their header, including when they are
// empty, because the `:config` page anchors jump to those two lines.
func Config(out io.Writer, in ConfigInput) error {
	w := &writer{out: out}

	w.printf(SourcesHeader+"%d):\n", len(in.Sources))
	for _, s := range in.Sources {
		w.printf("  %-8s %s\n", s.Kind, s.Path)
	}
	if len(in.Sources) == 0 {
		w.printf("  none — a10r is running on built-in defaults\n")
	}

	w.printf("\n"+WarningsHeader+"%d):\n", len(in.Warnings))
	for _, line := range in.Warnings {
		w.printf("  %s\n", line)
	}
	if len(in.Warnings) == 0 {
		w.printf("  none\n")
	}
	return w.err
}
