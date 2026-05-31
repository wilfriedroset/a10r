// SPDX-License-Identifier: Apache-2.0

// Package browser opens URLs in the user's default browser via the
// platform launcher. URL-scheme policy (refusing non-http(s)) lives
// at the call site, not here — this package is a dumb launcher.
package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

const xdgOpen = "xdg-open"

// System launches URLs through the OS default handler.
type System struct {
	run func(name string, args ...string) error
}

func (s System) Open(url string) error {
	name, args := opener(runtime.GOOS, url)
	if s.run != nil {
		return s.run(name, args...)
	}
	// Fire-and-forget launch: Start does not wait, so a context would
	// have nothing to cancel.
	if err := exec.Command(name, args...).Start(); err != nil { //nolint:noctx // fire-and-forget launch; url is scheme-validated at the call site.
		return fmt.Errorf("launch %s: %w", name, err)
	}
	return nil
}

// The empty "" before the URL on Windows is start's title argument —
// without it start treats a quoted URL as the window title.
func opener(goos, url string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "cmd", []string{"/c", "start", "", url}
	default:
		return xdgOpen, []string{url}
	}
}
