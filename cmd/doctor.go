// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/doctor"
	"github.com/wilfriedroset/a10r/internal/output"
)

// newDoctorCmd returns the `a10r doctor` subcommand. Runs a small
// suite of preflight checks against every configured backend and
// reports per-(backend, check) severity so an operator can confirm
// the runtime is healthy before launching the TUI.
func newDoctorCmd(flags *GlobalFlags) *cobra.Command {
	var (
		outputFmt string
		only      []string
	)
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run preflight health checks against every configured backend",
		Long: `Run a small suite of preflight checks (reachability, auth,
version-floor) against every backend listed in a10r.yaml and report
per-(backend, check) severity. Use --output=json|yaml to consume
the result from CI/CD scripts.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), flags, doctorOptions{
				Output: outputFmt,
				Only:   only,
			})
		},
	}
	cmd.Flags().StringVar(&outputFmt, "output", "", "output format: table, json, yaml")
	cmd.Flags().StringSliceVar(&only, "only", nil,
		"run only the named checks (comma-separated; default: full battery)")
	return cmd
}

// doctorOptions bundles the cobra flags so runDoctor stays
// testable without the cobra dependency.
type doctorOptions struct {
	Output string
	Only   []string
}

// runDoctor loads the config, builds clients, runs the checker
// suite (filtered by --only when set), and renders the results in
// the requested format. Fail-closed on config load errors —
// doctor without a config is just `a10r init`.
func runDoctor(ctx context.Context, out io.Writer, flags *GlobalFlags, opts doctorOptions) error {
	format, err := output.ParseFormat(opts.Output)
	if err != nil {
		return err
	}

	cfg, err := config.Load(loadOptsFromFlags(flags))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	checkers, err := selectCheckers(doctor.DefaultCheckers(), opts.Only)
	if err != nil {
		return err
	}

	clients, buildFailures := buildDoctorClients(cfg)

	results := doctor.Run(ctx, cfg.Backends, clients, checkers)
	results = append(buildFailures, results...)

	resolved := output.Resolve(format, isStdoutTerminal(out))
	return renderDoctor(out, results, resolved)
}

// buildDoctorClients constructs one backend.Client per configured
// backend. Unlike the TUI's buildClients this caller does not
// thread a debug logger — doctor is short-lived and the
// per-request log lines would only confuse the table output.
//
// Construction failures are returned as a parallel slice of
// SeverityError Results rather than written to stderr. The
// alternative — fmt.Fprintf to stderr — would corrupt the
// `--output=json|yaml` payload when the operator captures
// `2>&1` in CI. The returned client map carries nil for each
// failed backend so the bundled checks still emit per-backend
// rows downstream (with the same SeverityError severity).
func buildDoctorClients(cfg *config.Config) (map[string]backend.Client, []doctor.Result) {
	ua := userAgent(version, commit)
	clients := make(map[string]backend.Client, len(cfg.Backends))
	var failures []doctor.Result
	for _, be := range cfg.Backends {
		c, err := factory.Build(be, ua)
		if err != nil {
			failures = append(failures, doctor.Result{
				Backend:  be.Name,
				Check:    "build",
				Severity: doctor.SeverityError,
				Message:  err.Error(),
			})
			clients[be.Name] = nil
			continue
		}
		clients[be.Name] = c
	}
	return clients, failures
}

// selectCheckers filters all by name when only is non-empty. An
// unknown name returns an error rather than silently dropping the
// row — the user's --only flag is wrong, surface it loud.
//
// Output preserves the registration order of `all`, NOT the order
// the user supplied --only — the per-check semantics depend on
// reachability running first (auth's transport-failure downgrade
// assumes reachability already reported the same root cause). A
// user-driven order would break that contract silently.
func selectCheckers(all []doctor.Checker, only []string) ([]doctor.Checker, error) {
	if len(only) == 0 {
		return all, nil
	}
	wanted := make(map[string]struct{}, len(only))
	for _, name := range only {
		wanted[name] = struct{}{}
	}

	out := make([]doctor.Checker, 0, len(only))
	known := make([]string, 0, len(all))
	for _, c := range all {
		known = append(known, c.Name())
		if _, ok := wanted[c.Name()]; ok {
			out = append(out, c)
			delete(wanted, c.Name())
		}
	}
	if len(wanted) > 0 {
		// Pick a deterministic name to mention in the error;
		// iterating a map otherwise produces flaky messages.
		var first string
		for name := range wanted {
			if first == "" || name < first {
				first = name
			}
		}
		return nil, fmt.Errorf("--only: unknown check %q (known: %s)",
			first, strings.Join(known, ", "))
	}
	return out, nil
}

// renderDoctor dispatches to the chosen format. Table is the
// default; JSON/YAML serialise the []Result slice through the
// shared encoders.
func renderDoctor(out io.Writer, results []doctor.Result, format output.Format) error {
	switch format {
	case output.FormatJSON:
		return output.WriteJSON(out, results)
	case output.FormatYAML:
		return output.WriteYAML(out, results)
	case output.FormatTable:
		// Fall through to the table path below.
	}
	// Resolve has been applied upstream; explicit table or empty
	// (from a degenerate caller) renders here.
	tbl := output.Table{
		Cols: []string{"backend", "check", "severity", "message"},
		Rows: doctorRows(results),
	}
	return tbl.Write(out)
}

// doctorRows flattens results to the column shape the Table
// helper consumes. Sort by backend then by the registration
// order of the checker so multi-backend output reads naturally
// from top to bottom.
func doctorRows(results []doctor.Result) [][]string {
	rows := make([][]string, 0, len(results))
	for _, r := range results {
		rows = append(rows, []string{r.Backend, r.Check, r.Severity.String(), r.Message})
	}
	return rows
}

// isStdoutTerminal returns true only when out is os.Stdout AND
// stdout is connected to a TTY. Used to drive the table-vs-json
// default format. A test-injected bytes.Buffer (or any non-os.File)
// hits the default-pipe path.
func isStdoutTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	return output.IsTerminal(f)
}
