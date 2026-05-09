// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/doctor"
	"github.com/wilfriedroset/a10r/internal/output"
)

func TestSelectCheckers_Default(t *testing.T) {
	t.Parallel()

	got, err := selectCheckers(doctor.DefaultCheckers(), nil)
	require.NoError(t, err)
	require.Len(t, got, len(doctor.DefaultCheckers()))
}

func TestSelectCheckers_Filter(t *testing.T) {
	t.Parallel()

	got, err := selectCheckers(doctor.DefaultCheckers(), []string{"reachability", "auth"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "reachability", got[0].Name())
	require.Equal(t, "auth", got[1].Name())
}

func TestSelectCheckers_PreservesRegistrationOrder(t *testing.T) {
	t.Parallel()

	// User passes filters in reverse-registration order; output
	// must still be in registration order so reachability runs
	// first (auth's transport-failure downgrade depends on it).
	got, err := selectCheckers(doctor.DefaultCheckers(), []string{"version-floor", "reachability"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "reachability", got[0].Name(),
		"registration order wins over user-supplied --only order")
	require.Equal(t, "version-floor", got[1].Name())
}

func TestSelectCheckers_UnknownNameErrors(t *testing.T) {
	t.Parallel()

	_, err := selectCheckers(doctor.DefaultCheckers(), []string{"reachability", "nope"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown check "nope"`)
	require.Contains(t, err.Error(), "reachability")
	require.Contains(t, err.Error(), "version-floor")
}

func TestDoctorRows_Flattens(t *testing.T) {
	t.Parallel()

	results := []doctor.Result{
		{Backend: "prod", Check: "reachability", Severity: doctor.SeverityOK, Message: ""},
		{Backend: "prod", Check: "auth", Severity: doctor.SeverityError, Message: "401"},
	}
	rows := doctorRows(results)

	require.Len(t, rows, 2)
	require.Equal(t, []string{"prod", "reachability", "ok", ""}, rows[0])
	require.Equal(t, []string{"prod", "auth", "error", "401"}, rows[1])
}

func TestRenderDoctor_TableContainsHeadersAndRows(t *testing.T) {
	t.Parallel()

	results := []doctor.Result{
		{Backend: "prod", Check: "reachability", Severity: doctor.SeverityOK},
	}
	var buf bytes.Buffer
	require.NoError(t, renderDoctor(&buf, results, output.FormatTable))

	out := buf.String()
	require.Contains(t, out, "BACKEND")
	require.Contains(t, out, "SEVERITY")
	require.Contains(t, out, "prod")
	require.Contains(t, out, "ok")
}

func TestRenderDoctor_JSONEmitsStringSeverity(t *testing.T) {
	t.Parallel()

	results := []doctor.Result{
		{Backend: "prod", Check: "auth", Severity: doctor.SeverityError, Message: "401"},
	}
	var buf bytes.Buffer
	require.NoError(t, renderDoctor(&buf, results, output.FormatJSON))

	out := buf.String()
	require.Contains(t, out, `"severity": "error"`,
		"JSON severity must serialise as the lowercase string, not an int")
	require.Contains(t, out, `"backend": "prod"`)
	require.Contains(t, out, `"message": "401"`)
}

func TestRenderDoctor_YAMLEmitsStringSeverity(t *testing.T) {
	t.Parallel()

	results := []doctor.Result{
		{Backend: "prod", Check: "auth", Severity: doctor.SeverityError, Message: "401"},
	}
	var buf bytes.Buffer
	require.NoError(t, renderDoctor(&buf, results, output.FormatYAML))

	out := buf.String()
	require.Contains(t, out, "severity: error",
		"YAML severity must serialise as the lowercase string")
	require.Contains(t, out, "backend: prod")
}

func TestIsStdoutTerminal_NonFile(t *testing.T) {
	t.Parallel()

	// A bytes.Buffer is not an *os.File — must report false so
	// renderDoctor falls through to the json default for pipes.
	var buf bytes.Buffer
	require.False(t, isStdoutTerminal(&buf))
}

func TestSelectCheckers_ErrorMessageDeterministic(t *testing.T) {
	t.Parallel()

	// Two unknown names — the error must always pick the same
	// (alphabetically-first) one to mention so test output and
	// CI logs don't flake on map iteration order.
	_, err1 := selectCheckers(doctor.DefaultCheckers(), []string{"zoo", "alpha"})
	_, err2 := selectCheckers(doctor.DefaultCheckers(), []string{"zoo", "alpha"})
	require.Error(t, err1)
	require.Error(t, err2)
	require.Equal(t, err1.Error(), err2.Error())
	require.Contains(t, err1.Error(), `"alpha"`,
		"alphabetically-first unknown name surfaces in the error")
}
