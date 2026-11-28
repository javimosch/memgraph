package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Every shift of the text puts a different byte of "é" or "日" on the cut.
func TestShortenedTextStaysValidUTF8(t *testing.T) {
	for shift := range 6 {
		dir := t.TempDir()
		cfg := &Config{MemoryDir: dir}
		text := strings.Repeat("a", shift) + strings.Repeat("é日", 80)
		writeMemoryFromInput(cfg, text, "project", "p", "s", nil)

		files, err := filepath.Glob(filepath.Join(dir, "memory_*.md"))
		if err != nil || len(files) != 1 {
			t.Fatalf("%v %v", files, err)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.Valid(data) {
			t.Errorf("shift %d: the saved file is not valid UTF-8", shift)
		}
		for _, section := range parseSections("[s] " + text) {
			if !utf8.ValidString(section.Preview) {
				t.Errorf("shift %d: preview %q", shift, section.Preview)
			}
		}
	}
}

// edit and supersede write a shortened description into the memory file.
func TestEditAndSupersedeWriteValidUTF8(t *testing.T) {
	for shift := range 6 {
		dir := t.TempDir()
		cfg := &Config{MemoryDir: dir, ScopeResolved: true}
		writeMemoryFromInput(cfg, "start", "project", "p", "s", nil)
		files, err := filepath.Glob(filepath.Join(dir, "memory_*.md"))
		if err != nil || len(files) != 1 {
			t.Fatalf("%v %v", files, err)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(files[0]), "memory_"), ".md")
		text := strings.Repeat("a", shift) + strings.Repeat("é日", 40)
		check := func(what, reply string) {
			files, _ := filepath.Glob(filepath.Join(dir, "memory_*.md"))
			for _, f := range files {
				if data, err := os.ReadFile(f); err != nil || !utf8.Valid(data) {
					t.Errorf("shift %d: after %s, %s is not valid UTF-8 (%v; %s)", shift, what, filepath.Base(f), err, reply)
				}
			}
		}
		check("edit", mcpEdit(cfg, map[string]any{"id": id, "text": text}))
		check("supersede", mcpSupersede(cfg, map[string]any{"id": id, "text": text}))
	}
}
