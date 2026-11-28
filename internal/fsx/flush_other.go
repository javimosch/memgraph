//go:build !linux

package fsx

func flushFiles(_ string, paths []string) error { return syncEach(paths) }
