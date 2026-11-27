package fsx

import (
	"errors"
	"os"
	"path/filepath"
)

// flushEach is the batch size up to which flushing file by file is cheaper
// than flushing the whole filesystem.
const flushEach = 16

// Batch replaces and removes many files in one directory durably. Flushing
// each file costs milliseconds, so a large batch is flushed once, before any
// rename: after a crash every file holds its old or its new content.
type Batch struct {
	dir     string
	writes  []staged
	removes []string
}

type staged struct{ temp, path string }

func NewBatch(dir string) *Batch { return &Batch{dir: dir} }

// Write stages new content for dir/name. Nothing changes until Commit.
func (b *Batch) Write(name string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(b.dir, ".tmp-*")
	if err != nil {
		return err
	}
	b.writes = append(b.writes, staged{f.Name(), filepath.Join(b.dir, name)})
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(perm)
	}
	return errors.Join(err, f.Close())
}

// Remove stages the removal of dir/name.
func (b *Batch) Remove(name string) {
	b.removes = append(b.removes, filepath.Join(b.dir, name))
}

// Discard drops whatever has not been committed. It is safe after Commit.
func (b *Batch) Discard() {
	for _, w := range b.writes {
		os.Remove(w.temp)
	}
	b.writes, b.removes = nil, nil
}

// Commit makes the staged changes durable. It returns only once they are.
func (b *Batch) Commit() error {
	if len(b.writes)+len(b.removes) == 0 {
		return nil
	}
	temps := make([]string, len(b.writes))
	for i, w := range b.writes {
		temps[i] = w.temp
	}
	if err := flushFiles(b.dir, temps); err != nil {
		return err
	}
	for _, w := range b.writes {
		if err := os.Rename(w.temp, w.path); err != nil {
			return err
		}
	}
	for _, path := range b.removes {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	b.writes, b.removes = nil, nil
	return syncPath(b.dir)
}

func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func syncEach(paths []string) error {
	for _, p := range paths {
		if err := syncPath(p); err != nil {
			return err
		}
	}
	return nil
}
