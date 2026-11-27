// Package fsx holds the file primitives that keep local writers and cloud sync
// from clobbering each other.
package fsx

import (
	"os"
	"path/filepath"
)

// LockName is the lock file inside a directory. Sync never uploads it.
const LockName = ".memgraph.lock"

// WriteFileAtomic replaces path through a temp file and rename, so a reader
// never sees a partial file. The temp name cannot be mistaken for a memory.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Chmod(perm); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
