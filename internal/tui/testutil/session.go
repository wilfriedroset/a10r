// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/session"
)

// Session is writable with no guardrails.
func Session() *session.Session { return session.New(config.Config{}) }
