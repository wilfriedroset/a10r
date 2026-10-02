// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
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
	// Guardrail is the short refusal note ("denied", "max_bulk 20
	// exceeded") when the write policy would stop this tenant. A
	// read-only tenant never carries one: ADR 0049 answers read-only
	// first, so only one reason is ever named.
	Guardrail string `json:"guardrail,omitempty" yaml:"guardrail,omitempty"`
	// rawMatchers stays unescaped: lines mode quotes it, while the
	// structured modes keep the --matcher form.
	rawMatchers []backend.Matcher
}

// runDryRun renders the resolved write plan and returns without calling
// the mutating op (ADR 0046: the command minus its mutation). The
// plan's display verb is derived from action so the rendered word and
// the evaluated rule name can never drift apart. It runs after
// target-building and instead of runWrites, so every refusal lands on
// the plan before it lands on the exit code. The exit code is
// faithful: the plan exits with whatever the real run's pre-mutation
// phase would give it, and a target carrying a skip exits non-zero
// exactly as the real run would.
func runDryRun(
	out, errOut io.Writer,
	cfg *config.Config,
	format output.Format,
	action guardrail.Action,
	targets []writeTarget,
	globalReadOnly bool,
	confirmTenants []string,
) error {
	readOnly := make(map[string]bool, len(cfg.Backends))
	for _, be := range cfg.Backends {
		readOnly[be.Name] = be.ReadOnly
	}

	notes := guardrailNotes(cfg, action, targets, readOnly, globalReadOnly, confirmTenants)

	verb := strings.TrimPrefix(string(action), "silence.")
	plans := make([]plannedWrite, 0, len(targets))
	results := make([]writeResult, 0, len(targets))
	for _, t := range targets {
		ro := globalReadOnly || readOnly[t.tenant]
		p := plannedWriteFrom(t, verb, ro)
		p.Guardrail = notes[t.tenant]
		plans = append(plans, p)
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

	// Read-only is asked through the real run's own gate, so the code a
	// dry run promises cannot drift from the code an apply gives. It is
	// asked first because ADR 0049 answers read-only ahead of any rule.
	if err := ensureWritableTargets(globalReadOnly, cfg, targetTenants(targets)); err != nil {
		return newEmittedError(exitCodeFor(err), err)
	}

	// A guardrail refuses the whole command, so its code outranks the
	// per-target skip accounting. The notes are already on the plan the
	// user just read, which is why the error is marked emitted.
	if len(notes) > 0 {
		return newEmittedError(ExitGuardrailRefused,
			fmt.Errorf("%swould refuse %s; no silence would be written",
				guardrailPrefix, strings.Join(refusedTenants(targets, notes), ", ")))
	}
	return writeExitError(results, nil)
}

// guardrailNotes maps each refused tenant to its short note. The whole
// resolved target list is asked about, read-only targets included,
// because one entry per target is the count max_bulk compares against.
// A read-only tenant is then dropped from the notes rather than from
// the count: ADR 0049 puts read-only first, so a10r never names a user
// rule on a backend that was already refused for another reason.
func guardrailNotes(
	cfg *config.Config,
	action guardrail.Action,
	targets []writeTarget,
	readOnly map[string]bool,
	globalReadOnly bool,
	confirmTenants []string,
) map[string]string {
	if globalReadOnly || len(cfg.Guardrails) == 0 {
		return nil
	}
	notes := make(map[string]string)
	for _, r := range guardrailRefusals(cfg.Guardrails, action, targets, confirmTenants) {
		if readOnly[r.Tenant] {
			continue
		}
		notes[r.Tenant] = r.Note
	}
	return notes
}

// refusedTenants lists the refused tenants once each, in the order the
// targets first name them, so the error reads in the same order as the
// plan the user just read.
func refusedTenants(targets []writeTarget, notes map[string]string) []string {
	out := make([]string, 0, len(notes))
	for _, t := range targetTenants(targets) {
		if _, ok := notes[t]; ok {
			out = append(out, t)
		}
	}
	return out
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
		p.Matchers = renderMatchers(t.spec.Matchers, matcher.Format)
		p.rawMatchers = t.spec.Matchers
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

func renderMatchers(ms []backend.Matcher, render func(backend.Matcher) string) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, render(m))
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
	fmt.Fprintf(&b, "would %s %s", p.Action, lineSafe(p.Tenant))
	if p.ID != "" {
		fmt.Fprintf(&b, " %s", lineSafe(p.ID))
	}
	if len(p.rawMatchers) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(renderMatchers(p.rawMatchers, quoteForLine), ", "))
		if p.StartsAt != "" {
			fmt.Fprintf(&b, " from %s", p.StartsAt)
		}
		if p.EndsAt != "" {
			fmt.Fprintf(&b, " until %s", p.EndsAt)
		}
	}
	if p.Skip != "" {
		fmt.Fprintf(&b, " (skip: %s)", lineSafe(p.Skip))
	}
	if p.ReadOnly {
		b.WriteString(" [read-only: " + dryRunReadOnlyRefused + "]")
	}
	if p.Guardrail != "" {
		b.WriteString(" [guardrail: " + lineSafe(p.Guardrail) + "]")
	}
	return b.String()
}

// quoteForLine also cleans the name: ParseOne only trims it.
func quoteForLine(m backend.Matcher) string {
	return lineSafe(matcher.Quote(m))
}

// lineSafe replaces control runes with a space, as the backend edge does.
// Argv and config text never passes that edge.
func lineSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
