package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeIndexMemory(t *testing.T, dir, id, text string) string {
	t.Helper()
	path := filepath.Join(dir, "memory_"+id+".md")
	body := "---\nname: n" + id + "\ndescription: d\ntype: project\ncreated: 2026-01-01T00:00:00Z\n---\n\n" + text + "\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// indexHas loads the index the way every search does.
func indexHas(t *testing.T, dir, term string) []string {
	t.Helper()
	index, err := loadSearchIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for id := range index.TermFreq[term] {
		ids = append(ids, id)
	}
	return ids
}

// Files can change with no index rebuild: a rebuild that was killed, rsync,
// git, a hand edit, or auto_index turned off.
func TestSearchIndexNoticesFileChanges(t *testing.T) {
	dir := t.TempDir()
	first := writeIndexMemory(t, dir, "1", "alpha")
	if len(indexHas(t, dir, "alpha")) != 1 {
		t.Fatal("first build misses the memory")
	}

	writeIndexMemory(t, dir, "2", "bravo")
	if len(indexHas(t, dir, "bravo")) != 1 {
		t.Error("an added memory is missed")
	}

	// Same size, different time: what a one-word replacement looks like.
	writeIndexMemory(t, dir, "1", "gamma")
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(first, later, later); err != nil {
		t.Fatal(err)
	}
	if len(indexHas(t, dir, "gamma")) != 1 || len(indexHas(t, dir, "alpha")) != 0 {
		t.Error("an edit is missed")
	}

	// Same time, different size: an editor or copy that preserves times.
	writeIndexMemory(t, dir, "1", "gamma delta")
	if err := os.Chtimes(first, later, later); err != nil {
		t.Fatal(err)
	}
	if len(indexHas(t, dir, "delta")) != 1 {
		t.Error("an edit that kept the modification time is missed")
	}

	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if len(indexHas(t, dir, "gamma")) != 0 {
		t.Error("a deleted memory is still indexed")
	}
}

func TestSearchIndexIsReusedOnlyWhileCurrent(t *testing.T) {
	dir := t.TempDir()
	writeIndexMemory(t, dir, "1", "alpha")
	indexHas(t, dir, "alpha")
	path := filepath.Join(dir, "search_index.json")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	// Other files in the directory are not memories.
	if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	indexHas(t, dir, "alpha")
	if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(old) {
		t.Errorf("a current index was rebuilt: %v %v", info.ModTime(), err)
	}
}

// An index from an older version records no files, and a damaged one is not
// readable. Both are rebuilt.
func TestSearchIndexRebuildsUnverifiableFiles(t *testing.T) {
	for name, content := range map[string]string{
		"older version": `{"Version":1,"TermFreq":{},"Memories":{}}`,
		"cut off":       `{"Version":1,"TermFr`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeIndexMemory(t, dir, "1", "alpha")
			if err := os.WriteFile(filepath.Join(dir, "search_index.json"), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			if len(indexHas(t, dir, "alpha")) != 1 {
				t.Error("not rebuilt")
			}
		})
	}
}

// The index is replaced, never rewritten in place, so a process killed while
// saving leaves the old file whole.
func TestSaveSearchIndexReplacesTheFile(t *testing.T) {
	dir := t.TempDir()
	writeIndexMemory(t, dir, "1", "alpha")
	indexHas(t, dir, "alpha")
	path := filepath.Join(dir, "search_index.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeIndexMemory(t, dir, "2", "bravo")
	indexHas(t, dir, "bravo")
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("the index was rewritten in place")
	}
}
