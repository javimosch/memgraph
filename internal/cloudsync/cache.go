package cloudsync

// The local object store is a cache. Between syncs it needs only the last
// synced commit and its files: the next fetch names that commit, and a push
// sends only what is newer.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// snapshot returns a commit's tree hash and entries.
func (r *remote) snapshot(commit plumbing.Hash) (plumbing.Hash, []object.TreeEntry, error) {
	c, err := r.repo.CommitObject(commit)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	tree, err := c.Tree()
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	return c.TreeHash, tree.Entries, nil
}

// intact reads a stored object and checks that its content matches its name.
// Object files are not flushed to disk, so a crash can leave them incomplete.
func (r *remote) intact(h plumbing.Hash) (err error) {
	obj, err := r.repo.Storer.EncodedObject(plumbing.AnyObject, h)
	if err != nil {
		return err
	}
	rd, err := obj.Reader()
	if err != nil {
		return err
	}
	defer rd.Close()
	sum := plumbing.NewHasher(obj.Type(), obj.Size())
	if _, err := io.Copy(sum, rd); err != nil {
		return err
	}
	if sum.Sum() != h {
		return fmt.Errorf("object %s is damaged", h)
	}
	return nil
}

// repair empties the store if part of the last synced snapshot is missing or
// damaged. The server would never resend it, so the next fetch has to start
// over.
func (r *remote) repair(last string) (reset bool, err error) {
	if last == "" {
		return false, nil
	}
	commit := plumbing.NewHash(last)
	tree, entries, err := r.snapshot(commit)
	if err == nil {
		check := []plumbing.Hash{commit, tree}
		for _, e := range entries {
			check = append(check, e.Hash)
		}
		for _, h := range check {
			if err = r.intact(h); err != nil {
				break
			}
		}
	}
	if err == nil {
		return false, nil
	}
	if err := os.RemoveAll(r.dir); err != nil {
		return false, err
	}
	return true, r.open()
}

func (r *remote) loosePath(h plumbing.Hash) string {
	s := h.String()
	return filepath.Join(r.dir, "objects", s[:2], s[2:])
}

// prune reduces the store to the snapshot of last, as loose objects, so its
// size depends on the memories and not on how often they were synced. The order
// makes an interruption harmless: copy, then delete packs, then delete the rest.
func (r *remote) prune(last string) error {
	if last == "" {
		return nil
	}
	commit := plumbing.NewHash(last)
	tree, entries, err := r.snapshot(commit)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	keep := map[plumbing.Hash]bool{commit: true, tree: true}
	for _, e := range entries {
		keep[e.Hash] = true
	}
	packs, err := filepath.Glob(filepath.Join(r.dir, "objects", "pack", "*"))
	if err != nil {
		return err
	}
	if len(packs) > 0 {
		for h := range keep {
			if _, err := os.Stat(r.loosePath(h)); err == nil {
				continue
			}
			obj, err := r.repo.Storer.EncodedObject(plumbing.AnyObject, h)
			if err != nil {
				return fmt.Errorf("prune: %w", err)
			}
			// SetEncodedObject writes a loose object.
			if _, err := r.repo.Storer.SetEncodedObject(obj); err != nil {
				return fmt.Errorf("prune: %w", err)
			}
		}
		// Every kept object must exist outside the packs before they go.
		for h := range keep {
			if _, err := os.Stat(r.loosePath(h)); err != nil {
				return fmt.Errorf("prune: %w", err)
			}
		}
		for _, p := range packs {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	objects := filepath.Join(r.dir, "objects")
	err = filepath.WalkDir(objects, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		prefix := filepath.Base(filepath.Dir(path))
		if len(prefix) != 2 {
			return nil
		}
		if !keep[plumbing.NewHash(prefix+d.Name())] {
			return os.Remove(path)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	return r.open()
}
