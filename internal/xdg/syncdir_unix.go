//go:build !windows

// SPDX-License-Identifier: Apache-2.0

package xdg

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// syncDir flushes the directory entry the rename created. A caller
// that believes a write landed and finds it gone after a power cut
// is worse off than one that sees the error and retries, so the
// failure is returned rather than logged. A filesystem that refuses
// the call at all is the exception: SMB, 9p and some FUSE mounts
// answer ENOTSUP or EINVAL for a directory, there is no second way
// to ask, and failing the write would disable the remembered state
// for the whole run over a file that did land.
func syncDir(dir string, fsync func(*os.File) error) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("state open dir: %w", err)
	}
	if err := fsync(d); err != nil && !errors.Is(err, errors.ErrUnsupported) && !errors.Is(err, syscall.EINVAL) {
		_ = d.Close()
		return fmt.Errorf("state sync dir: %w", err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("state close dir: %w", err)
	}
	return nil
}
