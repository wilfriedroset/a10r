// SPDX-License-Identifier: Apache-2.0

package bulkop

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// SilenceRequest asks the policy about an `s` press. Lead follows the
// press rather than the rule, so only the marked fan-out has a name of
// its own; a cursor press reads back as the rule to edit.
func SilenceRequest(bulk bool, tenants ...string) guardrail.Request {
	req := guardrail.Request{Action: guardrail.ActionSilenceCreate, Tenants: tenants}
	if bulk {
		req.Lead = "bulk silence"
	}
	return req
}

// SilenceResultFlash formats the completed-round flash for a bulk
// silence-all fanout. noun is the pluralised unit ("alerts",
// "instances") used in the N>=2 wording; N==1 reads "silence created" /
// "silence failed" to match the single-form success flash. Bulk-expire
// keeps its own wording at the silences call site because its verb
// differs.
func SilenceResultFlash(total, success, failed int, noun string) tea.Cmd {
	if total == 1 {
		if success == 1 {
			return footer.ShowFlash(footer.FlashSuccess, "silence created")
		}
		return footer.ShowFlash(footer.FlashError, "silence failed")
	}
	if failed == 0 {
		return footer.ShowFlash(footer.FlashSuccess, fmt.Sprintf("silenced %d %s", success, noun))
	}
	if success == 0 {
		return footer.ShowFlash(footer.FlashError, fmt.Sprintf("silence failed for %d %s", failed, noun))
	}
	return footer.ShowFlash(footer.FlashWarn, fmt.Sprintf("silenced %d of %d — %d failed", success, total, failed))
}
