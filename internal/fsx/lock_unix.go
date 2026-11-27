//go:build unix

package fsx

import (
	"os"
	"path/filepath"
	"syscall"
)

// Lock takes an exclusive advisory lock on dir, creating dir if needed. It
// blocks until the lock is free. The lock is not reentrant: taking it twice in
// one process without unlocking deadlocks.
func Lock(dir string) (unlock func(), err error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, LockName), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
