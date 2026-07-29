package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteMemoryFromInput_WritesFile verifies that writeMemoryFromInput
// creates a memory_*.md file in the configured memory dir with the content
// embedded. This is the path that handleRemember's stdin reader feeds into.
func TestWriteMemoryFromInput_WritesFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{MemoryDir: dir}
	content := "Use real database instances in tests, not mocks"

	id := writeMemoryFromInput(cfg, content, "feedback", "myproject", "default", []string{"testing"})

	if id == "" {
		t.Fatal("expected non-empty memory ID")
	}
	path := filepath.Join(dir, "memory_"+id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("memory file not written: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, content) {
		t.Fatalf("memory file missing content %q, got:\n%s", content, body)
	}
	if !strings.Contains(body, "feedback") {
		t.Fatalf("memory file missing type 'feedback', got:\n%s", body)
	}
}

// TestWriteMemoryFromInput_DefaultType verifies that an empty memoryType
// falls back to the config's default_memory_type.
func TestWriteMemoryFromInput_DefaultType(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		MemoryDir:    dir,
		GlobalConfig: GlobalConfig{DefaultMemoryType: "note"},
	}
	id := writeMemoryFromInput(cfg, "some content", "", "", "default", nil)
	path := filepath.Join(dir, "memory_"+id+".md")
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "note") {
		t.Fatalf("expected default type 'note' in file, got:\n%s", string(data))
	}
}
