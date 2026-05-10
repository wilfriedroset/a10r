// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"bytes"
	"errors"
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
