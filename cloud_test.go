package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/javimosch/memgraph/internal/cloudsync"
)

func TestLocalFlagSurvivesRewritesAndMarksSnapshots(t *testing.T) {
	m := Memory{ID: "1", Name: "n", Type: "user", Created: time.Unix(0, 0), Content: "body", Local: true}
	text := formatMemoryFile(m)
	back := parseMemory(text, "memory_1.md")
	if !back.Local || formatMemoryFile(back) != text {
		t.Fatalf("local flag not kept:\n%s", formatMemoryFile(back))
	}
	// The two readers of the flag must agree.
	if !cloudsync.IsLocalOnly([]byte(text)) {
		t.Fatal("sync does not see the flag memgraph writes")
	}
	if !snapshotOf(back).Local {
		t.Fatal("ledger snapshot lost the flag, so the ledger would upload the content")
	}
	m.Local = false
	if strings.Contains(formatMemoryFile(m), "local:") {
		t.Fatal("unflagged memories must keep their original bytes")
	}
}

func TestSupersedingReplacementInheritsLocal(t *testing.T) {
	cfg := &Config{MemoryDir: t.TempDir()}
	old := Memory{ID: "old", Name: "old", Type: "user", Created: time.Unix(0, 0), Content: "secret belief", Local: true}
	if err := os.WriteFile(filepath.Join(cfg.MemoryDir, "memory_old.md"), []byte(formatMemoryFile(old)), 0644); err != nil {
		t.Fatal(err)
	}
	mcpSupersede(cfg, map[string]any{"id": "old", "text": "newer secret belief"})
	files, _ := filepath.Glob(filepath.Join(cfg.MemoryDir, "memory_*.md"))
	if len(files) != 2 {
		t.Fatalf("files %v", files)
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		if !cloudsync.IsLocalOnly(raw) {
			t.Errorf("%s is not local-only:\n%s", f, raw)
		}
	}
}

func TestWritersWaitForTheMemoryDirLock(t *testing.T) {
	cfg := &Config{MemoryDir: t.TempDir()}
	unlock := lockMemoryDir(cfg)
	done := make(chan struct{})
	go func() {
		mcpSave(cfg, map[string]any{"text": "waits"})
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("save ran while the directory was locked")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("save never ran")
	}
}

func TestIngestRefusesALinkedDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	memDir, skills := t.TempDir(), t.TempDir()
	keep := filepath.Join(memDir, "memory_keep.md")
	os.WriteFile(keep, []byte("keep"), 0644)
	os.MkdirAll(filepath.Join(skills, "demo"), 0755)
	os.WriteFile(filepath.Join(skills, "demo", "SKILL.md"), []byte("---\nname: demo\n---\nbody\n"), 0644)
	if err := cloudStore("p").SaveLink(cloudsync.Link{Project: "p", URL: "https://example.invalid/p.git", MemoryDir: memDir}); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{MemoryDir: memDir}
	for name, ingest := range map[string]func() error{
		"multi":  func() error { _, _, _, err := ingestMultiDir([]string{skills}, memDir, cfg); return err },
		"single": func() error { _, _, _, err := ingestSkillsDir(skills, memDir, "p", cfg); return err },
	} {
		if err := ingest(); err == nil || !strings.Contains(err.Error(), "cloud project") {
			t.Errorf("%s ingest: error = %v", name, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("ingest deleted a synced memory")
	}
}

func TestParseCloudArgs(t *testing.T) {
	a, err := parseCloudArgs([]string{"conflicts", "--json", "show", "--project=x", "2"})
	if err != nil || a.project != "x" || strings.Join(a.words, " ") != "conflicts show 2" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := parseCloudArgs([]string{"sync", "--allow-mas-delete"}); err == nil {
		t.Fatal("a mistyped option must not be ignored")
	}
}

func TestMarkingADeletedMemoryWithholdsItsHistory(t *testing.T) {
	cfg := &Config{MemoryDir: t.TempDir()}
	if err := appendLedger(cfg, LedgerEntry{Action: "delete", MemoryID: "123_4",
		Snapshot: &LedgerSnapshot{Content: "to be withheld"}}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"4": false, "nothing": false, "123_4": true} {
		got, err := withholdHistory(cfg.MemoryDir, id)
		if err != nil || got != want {
			t.Errorf("withholdHistory(%q) = %v, %v; want %v", id, got, err, want)
		}
	}
	raw, _ := os.ReadFile(ledgerPath(cfg))
	if public := string(cloudsync.PublicLedger(raw, nil)); public != "" {
		t.Fatalf("would upload: %s", public)
	}
	if !strings.Contains(string(raw), "to be withheld") {
		t.Fatal("local history lost")
	}
	// Asking twice adds nothing.
	withholdHistory(cfg.MemoryDir, "123_4")
	if again, _ := os.ReadFile(ledgerPath(cfg)); string(again) != string(raw) {
		t.Fatal("marker added twice")
	}
}
