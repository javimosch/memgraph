//go:build !unix

package cloudserver

import (
	"fmt"
	"os"
)

// Limit is unsupported here, so the server's startup check fails.
func Limit([]string) int {
	fmt.Fprintln(os.Stderr, "memgraph limit: not supported on this platform")
	return 125
}
