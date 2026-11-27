package main

import (
	"fmt"
	"os"

	"github.com/javimosch/memgraph/internal/fsx"
)

// lockMemoryDir serializes anything that changes memory files with other
// writers and with cloud sync. Take it once, at the top of an operation. If
// locking fails the operation still runs: a save must never depend on sync.
func lockMemoryDir(cfg *Config) (unlock func()) {
	unlock, err := fsx.Lock(cfg.MemoryDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memgraph: warning: could not lock %s: %v\n", cfg.MemoryDir, err)
		return func() {}
	}
	return unlock
}

// writeMemoryBytes replaces a memory file atomically.
func writeMemoryBytes(path string, data []byte) error {
	return fsx.WriteFileAtomic(path, data, 0644)
}
