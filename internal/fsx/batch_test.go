package fsx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBatch(t *testing.T) {
	// Both sides of the flush threshold.
	for _, n := range []int{1, flushEach + 5} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			dir := t.TempDir()
			old := filepath.Join(dir, "old")
			os.WriteFile(old, []byte("x"), 0644)
			b := NewBatch(dir)
			defer b.Discard()
			for i := range n {
				if err := b.Write(fmt.Sprint("f", i), []byte(fmt.Sprint(i)), 0644); err != nil {
					t.Fatal(err)
				}
			}
			b.Remove("old")
			if _, err := os.Stat(filepath.Join(dir, "f0")); err == nil {
				t.Fatal("visible before commit")
			}
			if _, err := os.Stat(old); err != nil {
				t.Fatal("removed before commit")
			}
			if err := b.Commit(); err != nil {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != n {
				t.Fatalf("%d entries, want %d", len(entries), n)
			}
			got, _ := os.ReadFile(filepath.Join(dir, fmt.Sprint("f", n-1)))
			info, _ := os.Stat(filepath.Join(dir, "f0"))
			if string(got) != fmt.Sprint(n-1) || info.Mode().Perm() != 0644 {
				t.Fatalf("content %q mode %v", got, info.Mode())
			}
		})
	}
}

func TestBatchDiscardLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	b := NewBatch(dir)
	b.Write("a", []byte("a"), 0644)
	b.Discard()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal(entries)
	}
}
