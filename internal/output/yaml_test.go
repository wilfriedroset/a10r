// SPDX-License-Identifier: Apache-2.0

package output

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWriteYAML_StructPayload(t *testing.T) {
	t.Parallel()

	type entry struct {
		Name string `yaml:"name"`
		URL  string `yaml:"url"`
	}
	var buf bytes.Buffer
	require.NoError(t, WriteYAML(&buf, []entry{
		{Name: "prod", URL: "http://am:9093"},
	}))

	out := buf.String()
	require.Contains(t, out, "name: prod")
	require.Contains(t, out, "url: http://am:9093")
}

func TestWriteYAML_RoundTrips(t *testing.T) {
	t.Parallel()

	// Round-trip via yaml.Unmarshal so the test asserts shape, not
	// exact byte sequence — yaml.v3 may legitimately tweak quoting
	// and key ordering across versions.
	in := map[string]any{"key": "value", "n": 42}
	var buf bytes.Buffer
	require.NoError(t, WriteYAML(&buf, in))

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "value", got["key"])
	require.Equal(t, 42, got["n"])
}

func TestWriteYAML_TwoSpaceIndent(t *testing.T) {
	t.Parallel()

	type nested struct {
		Outer struct {
			Inner string `yaml:"inner"`
		} `yaml:"outer"`
	}
	var v nested
	v.Outer.Inner = "x"
	var buf bytes.Buffer
	require.NoError(t, WriteYAML(&buf, v))

	require.Contains(t, buf.String(), "\n  inner: x")
}

// failingWriter returns errStub from every Write. Used to force
// yaml.Encoder into an error path that the WriteYAML wrapper can
// surface. yaml.v3 panics on unmarshallable Go types (channels,
// funcs); injecting a failing writer is the only path to exercise
// the encode error wrap without relying on panic recovery.
type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) { return 0, errStub }

var errStub = errors.New("stub failure")

func TestWriteYAML_WrapsEncodeError(t *testing.T) {
	t.Parallel()

	err := WriteYAML(failingWriter{}, map[string]string{"k": "v"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode yaml:")
}
