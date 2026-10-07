// SPDX-License-Identifier: Apache-2.0

package xdg

import "os"

// syncDir does nothing on Windows. FlushFileBuffers, which Sync
// calls, rejects a directory handle with ERROR_ACCESS_DENIED, so
// asking would turn every state write into a failure the callers
// treat as fatal. The rename itself is the durability guarantee the
// platform offers here.
func syncDir(string, func(*os.File) error) error { return nil }
