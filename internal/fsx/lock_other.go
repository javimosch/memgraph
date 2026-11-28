//go:build !unix

package fsx

import "errors"

// Lock is unsupported here. Local commands carry on unlocked; cloud sync
// refuses to run.
func Lock(string) (func(), error) {
	return func() {}, errors.New("file locking is not supported on this platform")
}
