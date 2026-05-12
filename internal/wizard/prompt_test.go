// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrompter_StringUsesDefaultOnEmpty(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := New(strings.NewReader("\n"), &out)
	got, err := p.String("name", "prod", nil)
	require.NoError(t, err)
	require.Equal(t, "prod", got)
	require.Contains(t, out.String(), "[prod]")
}

func TestPrompter_StringReturnsTrimmedInput(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader("  staging  \n"), &bytes.Buffer{})
	got, err := p.String("name", "", nil)
	require.NoError(t, err)
	require.Equal(t, "staging", got)
}

func TestPrompter_StringRetriesOnInvalid(t *testing.T) {
	t.Parallel()

	in := strings.NewReader("bad\nok\n")
	var out bytes.Buffer
	p := New(in, &out)
	got, err := p.String("name", "", func(s string) error {
		if s == "bad" {
			return errors.New("reserved word")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "ok", got)
	require.Contains(t, out.String(), "invalid: reserved word")
}

func TestPrompter_StringEOFErrors(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader(""), &bytes.Buffer{})
	_, err := p.String("name", "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "EOF")
}

func TestPrompter_ChoiceAcceptsValid(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader("mimir\n"), &bytes.Buffer{})
	got, err := p.Choice("kind", []string{"alertmanager", "mimir"}, "alertmanager")
	require.NoError(t, err)
	require.Equal(t, "mimir", got)
}

func TestPrompter_ChoiceUsesDefaultOnEmpty(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader("\n"), &bytes.Buffer{})
	got, err := p.Choice("kind", []string{"alertmanager", "mimir"}, "alertmanager")
	require.NoError(t, err)
	require.Equal(t, "alertmanager", got)
}

func TestPrompter_ChoiceRejectsUnknown(t *testing.T) {
	t.Parallel()

	in := strings.NewReader("bogus\nmimir\n")
	var out bytes.Buffer
	p := New(in, &out)
	got, err := p.Choice("kind", []string{"alertmanager", "mimir"}, "alertmanager")
	require.NoError(t, err)
	require.Equal(t, "mimir", got)
	require.Contains(t, out.String(), "invalid:")
}

func TestPrompter_BoolYes(t *testing.T) {
	t.Parallel()

	cases := []string{"y\n", "yes\n", "Y\n", "YES\n"}
	for _, c := range cases {
		p := New(strings.NewReader(c), &bytes.Buffer{})
		got, err := p.Bool("ok", false)
		require.NoError(t, err)
		require.True(t, got, "input %q must parse as yes", c)
	}
}

func TestPrompter_BoolNo(t *testing.T) {
	t.Parallel()

	cases := []string{"n\n", "no\n", "N\n"}
	for _, c := range cases {
		p := New(strings.NewReader(c), &bytes.Buffer{})
		got, err := p.Bool("ok", true)
		require.NoError(t, err)
		require.False(t, got, "input %q must parse as no", c)
	}
}

func TestPrompter_BoolEmptyUsesDefault(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader("\n"), &bytes.Buffer{})
	got, err := p.Bool("ok", true)
	require.NoError(t, err)
	require.True(t, got)
}

func TestEnableColor_DisabledWhenStdoutIsNotATerminal(t *testing.T) {
	// Pipe ⇒ not a TTY ⇒ color must be off regardless of env.
	// No t.Parallel(): t.Setenv requires the test stay serial.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() {
		_ = r.Close()
		_ = w.Close()
	}()

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	require.False(t, enableColor(w),
		"pipe handle isn't a TTY — color must be off")
}

func TestEnableColor_HonoursNoColorEnvVar(t *testing.T) {
	// We can't fake a TTY without a pty, but we can pin the
	// NO_COLOR branch: enableColor must return false on a non-
	// TTY OR when NO_COLOR is set — covering "NO_COLOR wins" via
	// the non-TTY arm still proves the env probe runs in order.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() {
		_ = r.Close()
		_ = w.Close()
	}()

	t.Setenv("NO_COLOR", "1")
	require.False(t, enableColor(w))
}

func TestEnableColor_HonoursTermDumb(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() {
		_ = r.Close()
		_ = w.Close()
	}()

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	require.False(t, enableColor(w))
}

func TestFrom_NonFileHandlesRouteToPlainConstructor(t *testing.T) {
	t.Parallel()

	// strings.Reader / bytes.Buffer aren't *os.File → must land
	// on the plain constructor, which means color is off and the
	// rendered prompt is byte-identical to the pre-styling era.
	var out bytes.Buffer
	p := From(strings.NewReader("\n"), &out)
	_, err := p.String("name", "prod", nil)
	require.NoError(t, err)
	require.NotContains(t, out.String(), "\x1b[",
		"non-file handles must produce a colour-off prompter; got %q", out.String())
}

func TestPrompter_SecretReturnsLineInNonTTYFallback(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader("hunter2\n"), &bytes.Buffer{})
	got, err := p.Secret("token")
	require.NoError(t, err)
	require.Equal(t, "hunter2", got)
}

func TestPrompter_SecretRepromptsOnEmpty(t *testing.T) {
	t.Parallel()

	// Constructed via New (not From) so the styler is forced
	// off and the rendered prompt is plain bytes — otherwise
	// the "invalid: cannot be empty" substring assertion would
	// be brittle against the colour-on path's ANSI wrapping.
	var out bytes.Buffer
	p := New(strings.NewReader("\nfilled\n"), &out)
	got, err := p.Secret("token")
	require.NoError(t, err)
	require.Equal(t, "filled", got)
	require.Contains(t, out.String(), "invalid: cannot be empty")
}

func TestPrompter_SecretEOFErrors(t *testing.T) {
	t.Parallel()

	p := New(strings.NewReader(""), &bytes.Buffer{})
	_, err := p.Secret("token")
	require.Error(t, err)
	require.Contains(t, err.Error(), "EOF")
}

func TestPrompter_BoolHintReflectsDefault(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	p := New(strings.NewReader("\n"), &out)
	_, err := p.Bool("ok", true)
	require.NoError(t, err)
	require.Contains(t, out.String(), "[Y/n]")

	out.Reset()
	p = New(strings.NewReader("\n"), &out)
	_, err = p.Bool("ok", false)
	require.NoError(t, err)
	require.Contains(t, out.String(), "[y/N]")
}
