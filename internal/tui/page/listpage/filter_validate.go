// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"errors"
	"fmt"

	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// textFilterValidate reports why s cannot be used as a `/` buffer on a
// text-only page, or nil when it can. The grammar leads the message so
// the title tag, the Enter flash and the `:alerts --filter` rejection
// all read the same.
func textFilterValidate(s string) error {
	if _, err := footer.NewMatcher(s); err != nil {
		return fmt.Errorf("regex: %s", footer.RegexErrText(err))
	}
	return nil
}

// LabelFilterValidate is the filter grammar for the pages that also
// accept a Prometheus label selector (alerts list, group detail). A
// buffer that is not a selector at all is judged as text; one that is
// a selector but carries an uncompilable regex is reported under the
// `matcher` tag so the user can tell which grammar rejected them.
func LabelFilterValidate(s string) error {
	_, err := matcher.LabelPredicate(s)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, matcher.ErrNotMatcher):
		return textFilterValidate(s)
	default:
		return fmt.Errorf("matcher: %s", footer.RegexErrText(err))
	}
}
