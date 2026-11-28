package fsx

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func flushFiles(dir string, paths []string) error {
	if len(paths) <= flushEach {
		return syncEach(paths)
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(unix.Syncfs(int(f.Fd())), f.Close())
}
