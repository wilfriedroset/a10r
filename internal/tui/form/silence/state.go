// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// parseSpec converts the form's field buffers into a
// backend.SilenceSpec. Returns the first validation error
// encountered. In bulk mode the matchers buffer is hidden and the
// resulting spec leaves Matchers empty; the parent page
// substitutes per-target matchers at fan-out.
func (f *Form) parseSpec() (backend.SilenceSpec, error) {
	matchers, err := matcher.Parse(f.matchers.Value())
	if err != nil {
		return backend.SilenceSpec{}, err
	}
	if !f.bulk && len(matchers) == 0 {
		return backend.SilenceSpec{}, errors.New("at least one matcher is required")
	}
	starts, err := parseTimeOrNow(f.starts.Value(), f.now())
	if err != nil {
		return backend.SilenceSpec{}, errors.New("starts: " + err.Error())
	}
	ends, err := parseEndsAt(f.ends.Value(), starts)
	if err != nil {
		return backend.SilenceSpec{}, errors.New("ends: " + err.Error())
	}
	if !ends.After(starts) {
		return backend.SilenceSpec{}, errors.New("ends must be after starts")
	}
	creator := strings.TrimSpace(f.creator.Value())
	if creator == "" {
		return backend.SilenceSpec{}, errors.New("creator is required")
	}
	comment := strings.TrimSpace(f.comment.Value())
	if comment == "" {
		return backend.SilenceSpec{}, errors.New("comment is required")
	}
	return backend.SilenceSpec{
		Matchers:  matchers,
		StartsAt:  starts,
		EndsAt:    ends,
		CreatedBy: creator,
		Comment:   comment,
	}, nil
}

// parseTimeOrNow returns now when in is empty / "now"; otherwise
// parses RFC3339.
func parseTimeOrNow(in string, now time.Time) (time.Time, error) {
	in = strings.TrimSpace(in)
	if in == "" || in == "now" {
		return now, nil
	}
	return time.Parse(time.RFC3339, in)
}

// parseEndsAt accepts either a Duration shorthand ("2h", "7d",
// "1w2d3h") relative to base, or an RFC3339 timestamp. Empty input
// is a validation error so the BlankEnds entry point
// (recreate-expired) can't be Ctrl+S'd through with no duration
// typed — the field's "2h" placeholder is a hint, not a default.
// The `n` and `e` flows pre-fill a non-empty value so they never
// hit the empty branch.
//
// Disambiguation: when the duration parse fails AND the input
// carries a letter that could be a unit attempt, the duration
// error wins so a `7days`-shaped typo surfaces the Duration
// shorthand grammar rather than the misleading RFC3339 layout
// error stdlib emits.
func parseEndsAt(in string, base time.Time) (time.Time, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return time.Time{}, errors.New("ends is required")
	}
	d, durErr := timerender.Parse(in)
	if durErr == nil {
		return base.Add(d), nil
	}
	if containsUnitLetter(in) {
		return time.Time{}, durErr
	}
	return time.Parse(time.RFC3339, in)
}

// containsUnitLetter reports whether s carries any letter the
// Duration shorthand grammar (or its rejected capitals) would
// recognise as a unit attempt. Drives the parseEndsAt
// disambiguation: a letter-bearing input means the operator was
// reaching for a duration, not an RFC3339 timestamp.
//
// The capital set is broader than the three the parser names in
// its tailored messages (M/W/Y). `D/H/S` aren't load-bearing for
// the friendly capital errors but a caps-locked operator typing
// `1D` should still see the duration error, not the RFC3339
// layout error — RFC3339 has no `D/H/S` letter anywhere, so any
// letter at all is enough to disambiguate the intent.
func containsUnitLetter(s string) bool {
	for i := range len(s) {
		switch s[i] {
		case 'w', 'd', 'h', 'm', 's', 'y',
			'W', 'D', 'H', 'M', 'S', 'Y':
			return true
		}
	}
	return false
}

// MatchersFromLabels turns a label-set into equality matchers,
// dropping the synthetic `__name__` key. Sorted by name so a
// prefilled form renders deterministically. Shared between the
// alerts list / alert detail / groups pages so all three build
// the same matchers from the same labels and a future change
// (different ignored keys, different operators) lands in one
// place. Lives here because the silenceform package is the only
// consumer of the output.
func MatchersFromLabels(labels map[string]string) []backend.Matcher {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]backend.Matcher, 0, len(keys))
	for _, k := range keys {
		out = append(out, backend.Matcher{
			Name: k, Value: labels[k], IsEqual: true,
		})
	}
	return out
}

// formatMatchers renders matchers in the same one-per-line syntax
// the user types manually so a prefilled form can be edited
// without a special path. Inverse of matcher.Parse — the symmetry
// is asserted by TestForm_FormatMatchersRoundTrip.
func formatMatchers(in []backend.Matcher) string {
	if len(in) == 0 {
		return ""
	}
	parts := make([]string, len(in))
	for i, m := range in {
		parts[i] = m.Name + matcher.Op(m) + m.Value
	}
	return strings.Join(parts, "\n")
}
