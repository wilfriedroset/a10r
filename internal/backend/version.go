// SPDX-License-Identifier: Apache-2.0

package backend

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MinAlertmanagerVersion is the lowest Alertmanager v2 release a10r
// supports. Below 0.28.1 some endpoints (notably /-/ready and the v2
// status shape) lack fields a10r reads. Doctor enforces this at
// startup so an operator running an older AM sees a precise error
// rather than a confusing 404 / decode failure on the first poll.
const MinAlertmanagerVersion = "0.28.1"

// Version is a parsed semver-ish triple. Alertmanager versions are
// strict semver (X.Y.Z, optional pre-release) per the project's
// release tags; only the Major/Minor/Patch components participate
// in the floor comparison. Pre-release strings ("0.28.1-rc.1") are
// treated as their underlying X.Y.Z and compared accordingly —
// running an rc against the floor is allowed, matching how
// operators upgrade.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion accepts the common shapes Alertmanager emits in its
// /api/v2/status payload's versionInfo.version field:
//
//   - "0.28.1"
//   - "v0.28.1" (some build chains prepend `v`)
//   - "0.28.1-rc.1" (release candidates)
//   - "0.28.1+build.42" (semver build metadata)
//
// Anything that does not start with three dotted integers fails
// fast with ErrInvalidVersion-prefixed error so the caller can
// surface "your Alertmanager reports an unrecognised version
// string" rather than silently treating it as 0.0.0.
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimPrefix(s, "v")
	// Drop pre-release / build-metadata suffix; the floor compares
	// X.Y.Z only.
	if i := strings.IndexAny(raw, "-+"); i >= 0 {
		raw = raw[:i]
	}
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w: %q (want X.Y.Z)", ErrInvalidVersion, s)
	}
	major, err := parseComponent("major", parts[0])
	if err != nil {
		return Version{}, err
	}
	minor, err := parseComponent("minor", parts[1])
	if err != nil {
		return Version{}, err
	}
	patch, err := parseComponent("patch", parts[2])
	if err != nil {
		return Version{}, err
	}
	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

// parseComponent atois one X.Y.Z component, rejecting non-integers
// and negatives with a uniformly-wrapped ErrInvalidVersion.
// Alertmanager release tags are non-negative; a `-1.0.0` slipping
// through is symptomatic of garbled wire data, not legitimate
// versioning.
func parseComponent(name, raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q: %w", ErrInvalidVersion, name, raw, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: %s %q must be non-negative", ErrInvalidVersion, name, raw)
	}
	return n, nil
}

// ErrInvalidVersion is returned by ParseVersion when the input
// does not match the X.Y.Z (with optional v-prefix or
// pre-release/build-metadata suffix) shape.
var ErrInvalidVersion = errors.New("invalid version")

// Compare returns -1 / 0 / +1 in the cmp.Compare convention. Used
// by doctor's version-floor check.
func (v Version) Compare(other Version) int {
	return cmp.Or(
		cmp.Compare(v.Major, other.Major),
		cmp.Compare(v.Minor, other.Minor),
		cmp.Compare(v.Patch, other.Patch),
	)
}

// String renders v back as X.Y.Z. Used in doctor messages so the
// rendered floor matches the parsed-and-stored value exactly.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}
