// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/output"
)

// plannedWrite is one resolved write target as a dry-run would render it
// (ADR 0046): the verb, the tenant it would land in, and the spec the
// real run would submit. Optional fields are omitted when empty so the
// structured output stays terse and a create (no id yet) reads cleanly.
type plannedWrite struct {
	Tenant    string   `json:"tenant" yaml:"tenant"`
	Action    string   `json:"action" yaml:"action"`
	ID        string   `json:"id,omitempty" yaml:"id,omitempty"`
	Matchers  []string `json:"matchers,omitempty" yaml:"matchers,omitempty"`
	StartsAt  string   `json:"starts_at,omitempty" yaml:"starts_at,omitempty"`
	EndsAt    string   `json:"ends_at,omitempty" yaml:"ends_at,omitempty"`
	Comment   string   `json:"comment,omitempty" yaml:"comment,omitempty"`
	CreatedBy string   `json:"created_by,omitempty" yaml:"created_by,omitempty"`
	Skip      string   `json:"skip,omitempty" yaml:"skip,omitempty"`
	ReadOnly  bool     `json:"read_only,omitempty" yaml:"read_only,omitempty"`
}

// runDryRun renders the resolved write plan and returns without calling
// the mutating op (ADR 0046: the command minus its mutation). It runs
// after target-building and instead of ensureWritableTargets/runWrites,
// so read-only is noted on the plan rather than aborting — with no
// mutation in flight the fail-closed gate is moot. The exit code is
// faithful: a target carrying a skip exits non-zero exactly as the real
// run would, a fully writable plan exits zero.
func runDryRun(
	out, errOut io.Writer,
	cfg *config.Config,
	format output.Format,
	action string,
	targets []writeTarget,
	globalReadOnly bool,
) error {
	readOnly := make(map[string]bool, len(cfg.Backends))
	for _, be := range cfg.Backends {
		readOnly[be.Name] = be.ReadOnly
	}

	plans := make([]plannedWrite, 0, len(targets))
	results := make([]writeResult, 0, len(targets))
	for _, t := range targets {
		ro := globalReadOnly || readOnly[t.tenant]
		plans = append(plans, plannedWriteFrom(t, action, ro))
		if t.skip != nil {
			results = append(results, writeResult{Tenant: t.tenant, ID: t.id, Status: writeStatusError, Error: t.skip.Error()})
			continue
		}
		results = append(results, writeResult{Tenant: t.tenant, ID: t.id, Status: writeStatusPlanned})
	}

	switch format {
	case output.FormatJSON:
		if err := output.WriteJSON(out, plans); err != nil {
			return fmt.Errorf("write json: %w", err)
		}
	case output.FormatYAML:
		if err := output.WriteYAML(out, plans); err != nil {
			return fmt.Errorf("write yaml: %w", err)
		}
	default:
		dryRunLines(out, errOut, plans)
	}

	return writeExitError(results, nil)
}

// plannedWriteFrom projects one resolved target onto its dry-run record:
// the id when minted (update/expire), the resolved spec when present
// (create/update/recreate), the pre-known skip, and the read-only flag.
func plannedWriteFrom(t writeTarget, action string, readOnly bool) plannedWrite {
	p := plannedWrite{Tenant: t.tenant, Action: action, ID: t.id, ReadOnly: readOnly}
	if t.skip != nil {
		p.Skip = t.skip.Error()
	}
	if len(t.spec.Matchers) > 0 {
		p.Matchers = renderMatchers(t.spec.Matchers)
		if !t.spec.StartsAt.IsZero() {
			p.StartsAt = t.spec.StartsAt.UTC().Format(time.RFC3339)
		}
		if !t.spec.EndsAt.IsZero() {
			p.EndsAt = t.spec.EndsAt.UTC().Format(time.RFC3339)
		}
		p.Comment = t.spec.Comment
		p.CreatedBy = t.spec.CreatedBy
	}
	return p
}

// renderMatchers prints each matcher as a Prometheus-style expr with the
// value quoted, matching the --matcher input syntax so the preview reads
// back as something the operator could retype.
func renderMatchers(ms []backend.Matcher) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name+matcher.Op(m)+strconv.Quote(m.Value))
	}
	return out
}

// dryRunLines renders the default human preview: one `would <action>`
// line per plan on stdout, and a single read-only note on stderr when any
// target sits in a read-only backend (the structured modes carry that on
// the per-plan read_only field instead).
func dryRunLines(out, errOut io.Writer, plans []plannedWrite) {
	anyReadOnly := false
	for _, p := range plans {
		fmt.Fprintln(out, dryRunLine(p))
		if p.ReadOnly {
			anyReadOnly = true
		}
	}
	if anyReadOnly {
		fmt.Fprintln(errOut, "note: read-only is active; "+dryRunReadOnlyRefused)
	}
}

// dryRunReadOnlyRefused is the shared phrase for a target the real run
// would refuse, used by both the stderr note and the lines-mode bracket so
// the two surfaces never drift.
const dryRunReadOnlyRefused = "apply would be refused"

func dryRunLine(p plannedWrite) string {
	var b strings.Builder
	fmt.Fprintf(&b, "would %s %s", p.Action, p.Tenant)
	if p.ID != "" {
		fmt.Fprintf(&b, " %s", p.ID)
	}
	if len(p.Matchers) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(p.Matchers, ", "))
		if p.StartsAt != "" {
			fmt.Fprintf(&b, " from %s", p.StartsAt)
		}
		if p.EndsAt != "" {
			fmt.Fprintf(&b, " until %s", p.EndsAt)
		}
	}
	if p.Skip != "" {
		fmt.Fprintf(&b, " (skip: %s)", p.Skip)
	}
	if p.ReadOnly {
		b.WriteString(" [read-only: " + dryRunReadOnlyRefused + "]")
	}
	return b.String()
}
