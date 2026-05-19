// SPDX-License-Identifier: Apache-2.0

package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/matcher"
)

func TestParseOne(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    backend.Matcher
		wantErr error
	}{
		{
			name: "equality with quotes",
			in:   `severity="critical"`,
			want: backend.Matcher{Name: "severity", Value: "critical", IsEqual: true},
		},
		{
			name: "equality without quotes",
			in:   "severity=critical",
			want: backend.Matcher{Name: "severity", Value: "critical", IsEqual: true},
		},
		{
			name: "negative equality with quotes",
			in:   `team!="infra"`,
			want: backend.Matcher{Name: "team", Value: "infra"},
		},
		{
			name: "negative equality bare",
			in:   "a!=b",
			want: backend.Matcher{Name: "a", Value: "b"},
		},
		{
			name: "regex equality with quotes",
			in:   `team=~"infra-.*"`,
			want: backend.Matcher{Name: "team", Value: "infra-.*", IsRegex: true, IsEqual: true},
		},
		{
			name: "regex equality bare",
			in:   "a=~.*",
			want: backend.Matcher{Name: "a", Value: ".*", IsRegex: true, IsEqual: true},
		},
		{
			name: "negative regex with quotes",
			in:   `team!~"infra-.*"`,
			want: backend.Matcher{Name: "team", Value: "infra-.*", IsRegex: true},
		},
		{
			name: "negative regex bare",
			in:   "a!~.*",
			want: backend.Matcher{Name: "a", Value: ".*", IsRegex: true},
		},
		{
			name: "two-char operator wins on tie at same index",
			in:   "foo=~bar",
			want: backend.Matcher{Name: "foo", Value: "bar", IsRegex: true, IsEqual: true},
		},
		{
			name: "leftmost wins for value containing an operator",
			in:   "foo=a!=b",
			want: backend.Matcher{Name: "foo", Value: "a!=b", IsEqual: true},
		},
		{
			name: "leftmost wins for value containing equals after !~",
			in:   "foo!~bar=baz",
			want: backend.Matcher{Name: "foo", Value: "bar=baz", IsRegex: true},
		},
		{name: "missing operator", in: "severitycritical", wantErr: matcher.ErrMissingOperator},
		{name: "empty string", in: "", wantErr: matcher.ErrMissingOperator},
		{name: "operator at index zero treated as missing", in: "=oops", wantErr: matcher.ErrMissingOperator},
		{name: "name only", in: `="critical"`, wantErr: matcher.ErrMissingOperator},
		{name: "missing value", in: "severity=", wantErr: matcher.ErrIncompleteMatcher},
		{name: "whitespace-only name", in: "   =critical", wantErr: matcher.ErrIncompleteMatcher},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := matcher.ParseOne(tc.in)
			if tc.wantErr != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParse_MultiLine(t *testing.T) {
	t.Parallel()
	in := "alertname=HighCPU\nseverity=~warning|critical\n\nteam!=platform"
	got, err := matcher.Parse(in)
	require.NoError(t, err)
	require.Equal(t, []backend.Matcher{
		{Name: "alertname", Value: "HighCPU", IsEqual: true},
		{Name: "severity", Value: "warning|critical", IsRegex: true, IsEqual: true},
		{Name: "team", Value: "platform"},
	}, got)
}

func TestParse_EmptyInputYieldsEmptySlice(t *testing.T) {
	t.Parallel()
	got, err := matcher.Parse("")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestParse_BadLineWrapsWithLineNumber(t *testing.T) {
	t.Parallel()
	_, err := matcher.Parse("a=1\nnoop\nb=2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "line 2:")
	require.Contains(t, err.Error(), "missing operator")
}

func TestOp_AllFourCombinations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		m    backend.Matcher
		want string
	}{
		{name: "literal equal", m: backend.Matcher{IsEqual: true}, want: "="},
		{name: "literal not equal", m: backend.Matcher{}, want: "!="},
		{name: "regex equal", m: backend.Matcher{IsRegex: true, IsEqual: true}, want: "=~"},
		{name: "regex not equal", m: backend.Matcher{IsRegex: true}, want: "!~"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, matcher.Op(tc.m))
		})
	}
}

func TestParseOne_ErrorsAreSentinelsForErrorsIs(t *testing.T) {
	t.Parallel()
	_, err := matcher.ParseOne("severitycritical")
	require.ErrorIs(t, err, matcher.ErrMissingOperator)

	_, err = matcher.ParseOne("severity=")
	require.ErrorIs(t, err, matcher.ErrIncompleteMatcher)
}
