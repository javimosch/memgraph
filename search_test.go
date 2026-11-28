package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeSearchMemory(t *testing.T, dir, id, tags, text string) string {
	t.Helper()
	path := filepath.Join(dir, "memory_"+id+".md")
	body := "---\nname: n" + id + "\ndescription: d\ntype: feedback\ntags: " + tags + "\ncreated: 2020-01-01T00:00:00Z\n---\n\n" + text + "\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func searchIDs(t *testing.T, dir, query string) string {
	t.Helper()
	results, err := searchMemoryDir(dir, query, SearchOptions{Weights: SearchWeights{TFIDF: 1, Phrase: 3, Exact: 2, Recency24h: 1, Recency7d: 1, Type: 1, Tag: 1.5}})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.MemoryID
	}
	return strings.Join(ids, " ")
}

func TestSearchMemoryDirQueries(t *testing.T) {
	dir := t.TempDir()
	writeSearchMemory(t, dir, "1", "red", "alpha bravo charlie")
	writeSearchMemory(t, dir, "2", "blue", "bravo alpha")
	writeSearchMemory(t, dir, "3", "red", "alpha alpha delta")
	writeSearchMemory(t, dir, "4", "blue", "unrelated words")

	for query, want := range map[string]string{
		"alpha":         "3 2 1", // 3 holds it twice; equals: newest first
		`"alpha bravo"`: "1",     // adjacent, in this order
		"alpha -delta":  "2 1",
		"char*":         "1",
		"bravo delta":   "3 2 1", // nothing has both, so partial matches
		"alpha charlie": "1",     // a whole match hides partial ones
		"tags:blue":     "4 2",
		"nosuchterm":    "",
	} {
		if got := searchIDs(t, dir, query); got != want {
			t.Errorf("%q: got %q, want %q", query, got, want)
		}
	}
}

// A term held by fewer memories weighs more, which needs the counts of the
// whole directory although memories are scored one at a time.
func TestSearchMemoryDirWeighsRareTerms(t *testing.T) {
	dir := t.TempDir()
	writeSearchMemory(t, dir, "1", "x", "rare")
	for _, id := range []string{"2", "3", "4", "5"} {
		writeSearchMemory(t, dir, id, "x", "common")
	}
	results, err := searchMemoryDir(dir, "rare", SearchOptions{Weights: SearchWeights{TFIDF: 1, Recency24h: 1, Recency7d: 1, Type: 1}})
	if err != nil || len(results) != 1 {
		t.Fatalf("%v %v", results, err)
	}
	// 1 for matching, plus 1 occurrence x log((5 memories + 1) / (1 + 1)).
	if want := 1 + math.Log(3); math.Abs(results[0].Score-want) > 1e-9 {
		t.Errorf("score %v, want %v", results[0].Score, want)
	}
}

// There is no index to fall behind the files, however they were changed.
func TestSearchSeesFilesAsTheyAre(t *testing.T) {
	dir := t.TempDir()
	first := writeSearchMemory(t, dir, "1", "x", "alpha")
	stale := filepath.Join(dir, "search_index.json")
	if err := os.WriteFile(stale, []byte(`{"Version":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := searchIDs(t, dir, "alpha"); got != "1" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the index of an older version is still there")
	}
	writeSearchMemory(t, dir, "2", "x", "bravo")
	if got := searchIDs(t, dir, "bravo"); got != "2" {
		t.Errorf("added: got %q", got)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if got := searchIDs(t, dir, "alpha"); got != "" {
		t.Errorf("deleted: got %q", got)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("an index was written")
	}
}

func TestSearchMissingDirectoryFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := searchMemoryDir(missing, "alpha", SearchOptions{}); err == nil {
		t.Error("search succeeded")
	}
	if _, err := loadMemories(missing); err == nil {
		t.Error("load succeeded")
	}
}

// A limit is applied while scanning. It must return what cutting the full
// list would, with superseded memories left out or not.
func TestSearchLimitMatchesCuttingTheFullList(t *testing.T) {
	dir := t.TempDir()
	for i := range 60 {
		id := fmt.Sprintf("%03d", i)
		text := "common " + strings.Repeat("extra ", i%7)
		path := writeSearchMemory(t, dir, id, "x", text)
		if i%5 == 0 {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), "type:", "superseded_by: 1\ntype:", 1))
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Nothing holds "rare" too, except one superseded memory.
	writeSearchMemory(t, dir, "100", "x\nsuperseded_by: 1", "common rare")

	ids := func(query string, opts SearchOptions) []string {
		opts.Weights = SearchWeights{TFIDF: 1, Recency24h: 1, Recency7d: 1, Type: 1}
		results, err := searchMemoryDir(dir, query, opts)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range results {
			if opts.HideSuperseded && r.SupersededBy != "" {
				t.Errorf("%q returned a superseded memory", query)
			}
			out = append(out, r.MemoryID)
		}
		return out
	}
	// "extra rare" has no whole match here, so these are partial ones.
	for _, query := range []string{"common", "extra", "extra nosuchterm"} {
		for _, hide := range []bool{false, true} {
			full := ids(query, SearchOptions{HideSuperseded: hide})
			for _, limit := range []int{1, 2, 5, 17, 100} {
				got := ids(query, SearchOptions{HideSuperseded: hide, Limit: limit})
				want := full[:min(limit, len(full))]
				if !slices.Equal(got, want) {
					t.Errorf("%q hide=%v limit=%d: got %v, want %v", query, hide, limit, got, want)
				}
			}
		}
	}
	// The only whole match is hidden: partial ones still do not take its place.
	if got := ids("common rare", SearchOptions{HideSuperseded: true}); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}
