// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/guardrail"
)

func TestGuardrails_LoadFixture(t *testing.T) {
	t.Parallel()

	cfg, err := loadWithEnv(LoadOpts{Dir: "testdata", File: "valid_guardrails.yaml"},
		func(string) string { return "stub" }, func() (string, error) { return "/u", nil }, "linux")
	require.NoError(t, err)

	require.Equal(t, guardrail.Set{
		{
			Tenants: []string{"prod-*"},
			Actions: []string{"silence.expire"},
			Deny:    true,
			Reason:  "expire prod silences from the change ticket, not a10r",
		},
		{
			Tenants:      []string{"prod-*"},
			Confirmation: guardrail.ConfirmationTypeTenantName,
		},
		{
			Tenants: []string{"*"},
			MaxBulk: 20,
		},
	}, cfg.Guardrails)
}

// TestGuardrails_RejectedAtLoad pins the wiring: Config.Validate
// delegates to the evaluator, and the loader wraps the rule error with
// the file that declared it. The rule table itself lives in
// internal/guardrail.
func TestGuardrails_RejectedAtLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
backends:
  - name: prod-eu
    url: https://am.internal
guardrails:
  - actions: ["silence.delete"]
    deny: true
`), 0o600))

	_, err := loadWithEnv(LoadOpts{Dir: dir},
		func(string) string { return "" }, func() (string, error) { return "/u", nil }, "linux")
	require.EqualError(t, err, `validate config "`+path+
		`": guardrails[0].actions[0]: unknown action "silence.delete" (known: silence.create, silence.update, silence.expire, silence.recreate)`)
}

// TestGuardrails_UnknownFieldIsRejected pins the strict-decode
// contract onto the new block: a typo inside a rule must name the
// line and column, not silently drop the restriction the operator
// wrote.
func TestGuardrails_UnknownFieldIsRejected(t *testing.T) {
	t.Parallel()

	_, err := decodeStrict([]byte(`
guardrails:
  - tenants: ["prod-*"]
    denyy: true
`))
	require.ErrorContains(t, err, "line 4")
	require.ErrorContains(t, err, "denyy")
}

// TestGuardrails_DropInsAppend pins the merge rule: a fragment adds
// restrictions and can never drop one the base declared, so the two
// layers concatenate rather than the last one winning.
func TestGuardrails_DropInsAppend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a10r.yaml"), []byte(`
backends:
  - name: prod-eu
    url: https://am.internal
guardrails:
  - tenants: ["prod-*"]
    deny: true
`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, configDropInDir), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, configDropInDir, "10-cap.yaml"), []byte(`
guardrails:
  - max_bulk: 5
`), 0o600))

	cfg, err := loadWithEnv(LoadOpts{Dir: dir},
		func(string) string { return "" }, func() (string, error) { return "/u", nil }, "linux")
	require.NoError(t, err)
	require.Equal(t, guardrail.Set{
		{Tenants: []string{"prod-*"}, Deny: true},
		{MaxBulk: 5},
	}, cfg.Guardrails)
}
