//go:build unix

package fsx

import (
	"fmt"
	"os"
	"syscall"
)

// TryLockFile takes an exclusive lock on path without waiting.
func TryLockFile(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s is locked by another process: %w", path, err)
	}
	return func() { f.Close() }, nil
}
