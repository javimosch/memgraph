//go:build !unix

package fsx

import "errors"

// TryLockFile is unsupported here.
func TryLockFile(string) (func(), error) {
	return nil, errors.New("file locking is not supported on this platform")
}
