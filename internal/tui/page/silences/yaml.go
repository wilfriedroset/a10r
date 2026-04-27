// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// silenceYAML is the wire shape the editor handoff round-trips
// through. Mirrors backend.Silence + backend.Matcher with public
// yaml tags; types stay internal to the silences package because
// no other consumer needs the shape today. RFC3339 timestamps
// keep the on-disk format readable enough for `:%s/2h/4h/g`-style
// edits.
type silenceYAML struct {
	ID        string        `yaml:"id"`
	Comment   string        `yaml:"comment"`
	CreatedBy string        `yaml:"createdBy"`
	StartsAt  string        `yaml:"startsAt"`
	EndsAt    string        `yaml:"endsAt"`
	Matchers  []matcherYAML `yaml:"matchers"`
}

// matcherYAML mirrors backend.Matcher one-for-one. IsEqual /
// IsRegex stay as separate booleans (rather than a packed
// operator string) so editor edits to a single field don't have
// to know the operator parser.
type matcherYAML struct {
	Name    string `yaml:"name"`
	Value   string `yaml:"value"`
	IsRegex bool   `yaml:"isRegex"`
	IsEqual bool   `yaml:"isEqual"`
}

// silenceToYAML marshals a backend.Silence into the on-disk shape
// the editor opens. Empty input renders an empty document — the
// caller decides whether that's a no-op.
func silenceToYAML(s backend.Silence) ([]byte, error) {
	doc := silenceYAML{
		ID:        s.ID,
		Comment:   s.Comment,
		CreatedBy: s.CreatedBy,
		StartsAt:  s.StartsAt.UTC().Format(time.RFC3339),
		EndsAt:    s.EndsAt.UTC().Format(time.RFC3339),
		Matchers:  make([]matcherYAML, len(s.Matchers)),
	}
	for i, m := range s.Matchers {
		doc.Matchers[i] = matcherYAML{
			Name: m.Name, Value: m.Value,
			IsRegex: m.IsRegex, IsEqual: m.IsEqual,
		}
	}
	return yaml.Marshal(doc)
}

// silenceFromYAML parses the editor's post-edit buffer into a
// silence ID + SilenceSpec ready for UpdateSilence. Validates the
// minimum the API requires (at least one matcher; ends after
// starts; non-empty creator + comment) so a malformed edit
// flashes a precise error instead of round-tripping a 400 from
// the backend.
func silenceFromYAML(in []byte) (string, backend.SilenceSpec, error) {
	if strings.TrimSpace(string(in)) == "" {
		return "", backend.SilenceSpec{}, errors.New("empty document")
	}
	var doc silenceYAML
	if err := yaml.Unmarshal(in, &doc); err != nil {
		return "", backend.SilenceSpec{}, err
	}
	if doc.ID == "" {
		return "", backend.SilenceSpec{}, errors.New("id is required")
	}
	if len(doc.Matchers) == 0 {
		return "", backend.SilenceSpec{}, errors.New("at least one matcher is required")
	}
	starts, err := time.Parse(time.RFC3339, strings.TrimSpace(doc.StartsAt))
	if err != nil {
		return "", backend.SilenceSpec{}, fmt.Errorf("startsAt: %w", err)
	}
	ends, err := time.Parse(time.RFC3339, strings.TrimSpace(doc.EndsAt))
	if err != nil {
		return "", backend.SilenceSpec{}, fmt.Errorf("endsAt: %w", err)
	}
	if !ends.After(starts) {
		return "", backend.SilenceSpec{}, errors.New("endsAt must be after startsAt")
	}
	if strings.TrimSpace(doc.CreatedBy) == "" {
		return "", backend.SilenceSpec{}, errors.New("createdBy is required")
	}
	if strings.TrimSpace(doc.Comment) == "" {
		return "", backend.SilenceSpec{}, errors.New("comment is required")
	}
	matchers := make([]backend.Matcher, len(doc.Matchers))
	for i, m := range doc.Matchers {
		matchers[i] = backend.Matcher{
			Name: m.Name, Value: m.Value,
			IsRegex: m.IsRegex, IsEqual: m.IsEqual,
		}
	}
	return doc.ID, backend.SilenceSpec{
		Matchers:  matchers,
		StartsAt:  starts,
		EndsAt:    ends,
		CreatedBy: strings.TrimSpace(doc.CreatedBy),
		Comment:   strings.TrimSpace(doc.Comment),
	}, nil
}
