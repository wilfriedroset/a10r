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

func TestLabelPredicate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		ok      bool
		labels  map[string]string
		matches bool
	}{
		{name: "equal matches exact value", input: "cluster_id=99", ok: true, labels: map[string]string{"cluster_id": "99"}, matches: true},
		{name: "equal rejects different value", input: "cluster_id=99", ok: true, labels: map[string]string{"cluster_id": "991"}, matches: false},
		{name: "equal rejects absent label", input: "cluster_id=99", ok: true, labels: map[string]string{}, matches: false},
		{name: "regex is fully anchored (full match)", input: "cluster_id=~9.*", ok: true, labels: map[string]string{"cluster_id": "99"}, matches: true},
		{name: "regex anchored rejects partial", input: "cluster_id=~9", ok: true, labels: map[string]string{"cluster_id": "99"}, matches: false},
		{name: "regex is case-sensitive (AM semantics, no implicit (?i))", input: "cluster_id=~ABC", ok: true, labels: map[string]string{"cluster_id": "abc"}, matches: false},
		{name: "embedded anchor compiles and stays well-formed", input: "cluster_id=~^9", ok: true, labels: map[string]string{"cluster_id": "9"}, matches: true},
		{name: "comma ANDs two matchers (both satisfied)", input: "cluster_id=99,role=consul", ok: true, labels: map[string]string{"cluster_id": "99", "role": "consul"}, matches: true},
		{name: "comma AND fails when one matcher fails", input: "cluster_id=99,role=consul", ok: true, labels: map[string]string{"cluster_id": "99", "role": "vault"}, matches: false},
		{name: "&& separates matchers too", input: "cluster_id=99 && role=consul", ok: true, labels: map[string]string{"cluster_id": "99", "role": "consul"}, matches: true},
		{name: "spaces around separators are trimmed", input: "cluster_id=99 , role=consul", ok: true, labels: map[string]string{"cluster_id": "99", "role": "consul"}, matches: true},
		{name: "mixed matcher + bare word falls back to text", input: "cluster_id=99,foo", ok: false},
		{name: "trailing comma falls back to text", input: "cluster_id=99,", ok: false},
		{name: "comma inside a regex value falls back to text", input: "cluster_id=~(a,b)", ok: false},
		{name: "not-equal matches different", input: "cluster_id!=99", ok: true, labels: map[string]string{"cluster_id": "98"}, matches: true},
		{name: "not-equal matches absent (empty != value)", input: "cluster_id!=99", ok: true, labels: map[string]string{}, matches: true},
		{name: "not-equal rejects same", input: "cluster_id!=99", ok: true, labels: map[string]string{"cluster_id": "99"}, matches: false},
		{name: "regex not-match rejects a match", input: "cluster_id!~prod-.*", ok: true, labels: map[string]string{"cluster_id": "prod-1"}, matches: false},
		{name: "regex not-match keeps a non-match", input: "cluster_id!~prod-.*", ok: true, labels: map[string]string{"cluster_id": "stg-1"}, matches: true},
		{name: "quoted value parses", input: `cluster_id="99"`, ok: true, labels: map[string]string{"cluster_id": "99"}, matches: true},
		{name: "bare word is text mode", input: "web", ok: false},
		{name: "fuzzy sigil is text mode", input: "~foo", ok: false},
		{name: "literal sigil is text mode", input: `\foo=1`, ok: false},
		{name: "leading operator is text mode", input: "=99", ok: false},
		{name: "empty is text mode", input: "", ok: false},
		{name: "uncompilable regex falls back to text", input: "cluster_id=~[", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pred, ok := matcher.LabelPredicate(tt.input)
			require.Equal(t, tt.ok, ok, "label-vs-text classification")
			if !tt.ok {
				require.Nil(t, pred)
				return
			}
			require.Equal(t, tt.matches, pred(tt.labels))
		})
	}
}
