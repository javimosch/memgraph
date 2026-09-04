package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestMemory(t *testing.T, dir, id, content string) {
	t.Helper()
	memory := Memory{
		ID: id, Name: "Memory " + id, Description: content, Type: "project",
		Created: time.Now().UTC(), Content: content,
	}
	if err := os.WriteFile(filepath.Join(dir, "memory_"+id+".md"), []byte(formatMemoryFile(memory)), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSupersessionSurvivesTheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeTestMemory(t, dir, "111", "stampd is live at stampd.intrane.fr")
	path := filepath.Join(dir, "memory_111.md")

	raw, _ := os.ReadFile(path)
	memory := parseMemory(string(raw), "memory_111.md")
	memory.SupersededBy = "222"
	memory.SupersededAt = time.Now().UTC().Truncate(time.Second)
	memory.SupersededReason = "retired into comptoir as sdlt"
	if err := os.WriteFile(path, []byte(formatMemoryFile(memory)), 0644); err != nil {
		t.Fatal(err)
	}

	raw, _ = os.ReadFile(path)
	back := parseMemory(string(raw), "memory_111.md")
	if back.SupersededBy != "222" {
		t.Errorf("superseded_by lost: %q", back.SupersededBy)
	}
	if !back.SupersededAt.Equal(memory.SupersededAt) {
		t.Errorf("superseded_at lost: %v want %v", back.SupersededAt, memory.SupersededAt)
	}
	if back.SupersededReason != memory.SupersededReason {
		t.Errorf("superseded_reason lost: %q", back.SupersededReason)
	}
	// The whole point: the replaced belief is still readable.
	if !strings.Contains(back.Content, "stampd is live") {
		t.Errorf("superseding destroyed the old belief: %q", back.Content)
	}
}

func TestLedgerIsAppendOnlyAndOutlivesItsSubject(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{MemoryDir: dir}

	first := LedgerEntry{Action: "supersede", MemoryID: "111", Target: "222", Reason: "moved"}
	if err := appendLedger(cfg, first); err != nil {
		t.Fatal(err)
	}
	second := LedgerEntry{
		Action: "delete", MemoryID: "111", Reason: "cleanup",
		Snapshot: &LedgerSnapshot{Name: "Memory 111", Content: "stampd is live at stampd.intrane.fr"},
	}
	if err := appendLedger(cfg, second); err != nil {
		t.Fatal(err)
	}

	entries, err := readLedger(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Action != "supersede" || entries[1].Action != "delete" {
		t.Errorf("entries are not in append order: %+v", entries)
	}
	if entries[0].Timestamp == "" || entries[0].Actor == "" {
		t.Errorf("timestamp and actor should be stamped automatically: %+v", entries[0])
	}
	// The memory is gone; the ledger still holds what it said.
	if entries[1].Snapshot == nil || !strings.Contains(entries[1].Snapshot.Content, "stampd is live") {
		t.Errorf("snapshot did not outlive the memory: %+v", entries[1].Snapshot)
	}
}

func TestReadLedgerSkipsCorruptLinesWithoutLosingHistory(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{MemoryDir: dir}
	if err := appendLedger(cfg, LedgerEntry{Action: "supersede", MemoryID: "111"}); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(ledgerPath(cfg), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString("{not json\n")
	file.Close()

	entries, err := readLedger(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].MemoryID != "111" {
		t.Fatalf("a corrupt tail must not hide the history before it: %+v", entries)
	}
}

func TestReadLedgerOnMissingFileIsEmptyNotAnError(t *testing.T) {
	entries, err := readLedger(&Config{MemoryDir: t.TempDir()})
	if err != nil {
		t.Fatalf("a scope with no governance actions is not an error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("want no entries, got %d", len(entries))
	}
}

func TestParseSinceAcceptsWindowsAndDates(t *testing.T) {
	if _, ok := parseSince("7d"); !ok {
		t.Error("7d should parse")
	}
	if _, ok := parseSince("2026-07-29"); !ok {
		t.Error("a date should parse")
	}
	if _, ok := parseSince("2026-07-29T10:00:00Z"); !ok {
		t.Error("RFC3339 should parse")
	}
	if _, ok := parseSince("last tuesday"); ok {
		t.Error("prose should be rejected, not silently treated as the epoch")
	}
}

func TestFilterSupersededHidesReplacedBeliefs(t *testing.T) {
	results := []SearchResult{
		{MemoryID: "a"},
		{MemoryID: "b", SupersededBy: "c"},
		{MemoryID: "c"},
	}
	kept := filterSuperseded(append([]SearchResult(nil), results...), false)
	if len(kept) != 2 || kept[0].MemoryID != "a" || kept[1].MemoryID != "c" {
		t.Fatalf("want a and c, got %+v", kept)
	}
	all := filterSuperseded(append([]SearchResult(nil), results...), true)
	if len(all) != 3 {
		t.Fatalf("--include-superseded should keep everything, got %+v", all)
	}
}
