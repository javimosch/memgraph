package main

import (
	"os"
	"os/exec"
	"strings"
)

// Tests that run "go run ." give it a temporary HOME. Pin Go's caches to their
// real locations, or each run downloads the dependencies into that HOME, as
// read-only files the test cannot clean up.
func init() {
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		if os.Getenv(key) != "" {
			continue
		}
		if out, err := exec.Command("go", "env", key).Output(); err == nil {
			os.Setenv(key, strings.TrimSpace(string(out)))
		}
	}
}
