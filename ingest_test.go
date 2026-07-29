package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseSyncDirs verifies comma-separated sync dirs are split and trimmed.
func TestParseSyncDirs(t *testing.T) {
	got := parseSyncDirs("a, b ,,c")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

// TestParseSyncDirs_Empty verifies an empty string yields no dirs.
func TestParseSyncDirs_Empty(t *testing.T) {
	if got := parseSyncDirs(""); len(got) != 0 {
		t.Fatalf("expected empty slice for empty input, got %v", got)
	}
	if got := parseSyncDirs(" , , "); len(got) != 0 {
		t.Fatalf("expected empty slice for whitespace-only input, got %v", got)
	}
}

// TestExpandHomeDir verifies ~ expansion.
func TestExpandHomeDir(t *testing.T) {
	// Non-home paths pass through unchanged.
	if got := expandHomeDir("/etc/passwd"); got != "/etc/passwd" {
		t.Fatalf("expected /etc/passwd unchanged, got %q", got)
	}
	// ~/something should be expanded (we only assert it no longer starts with ~).
	got := expandHomeDir("~/memgraph")
	if len(got) < 2 || got[:2] == "~/" {
		t.Fatalf("expected ~/ to be expanded, got %q", got)
	}
}

// TestIngestMultiDir_EndToEnd verifies the full ingest pipeline: scan a
// fixture directory of markdown files, build the graph, write memory files +
// graph.json, and reload the graph index. This is the regression baseline for
// the v1.3.x "all serve API errors return JSON" + graph build contract.
func TestIngestMultiDir_EndToEnd(t *testing.T) {
	srcDir := t.TempDir()
	// Create two skill-like markdown files in distinct "project" dirs.
	// deriveNamespace uses a regex on the dir name; we mimic real skill dirs.
	alphaDir := filepath.Join(srcDir, "alpha-tool")
	if err := os.MkdirAll(alphaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "SKILL.md"), []byte("---\nname: Alpha\ndescription: memory tool for agents\n---\nAlpha skill body.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	betaDir := filepath.Join(srcDir, "beta-tool")
	if err := os.MkdirAll(betaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaDir, "SKILL.md"), []byte("---\nname: Beta\ndescription: memory tool for agents\n---\nBeta skill body.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	targetDir := t.TempDir()
	cfg := &Config{MemoryDir: targetDir}
	graph, skillCount, namespaceCount, err := ingestMultiDir([]string{srcDir}, targetDir, cfg)
	if err != nil {
		t.Fatalf("ingestMultiDir: %v", err)
	}
	if skillCount != 2 {
		t.Fatalf("expected 2 skills, got %d", skillCount)
	}
	if namespaceCount < 1 {
		t.Fatalf("expected >= 1 namespace, got %d", namespaceCount)
	}
	if graph == nil || len(graph.Nodes) == 0 {
		t.Fatalf("expected non-empty graph, got %+v", graph)
	}

	// graph.json should exist and be reloadable.
	loaded, err := loadGraphIndex(targetDir)
	if err != nil {
		t.Fatalf("loadGraphIndex after ingest: %v", err)
	}
	if len(loaded.Nodes) == 0 {
		t.Fatalf("loaded graph has no nodes")
	}

	// Memory files should have been written for each node.
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	memFiles := 0
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > len("memory_.md") && e.Name()[:7] == "memory_" && e.Name()[len(e.Name())-3:] == ".md" {
			memFiles++
		}
	}
	if memFiles == 0 {
		t.Fatalf("expected memory_*.md files in target dir, found none")
	}
}

// TestParseMinimalFrontmatter verifies YAML-ish frontmatter parsing.
func TestParseMinimalFrontmatter(t *testing.T) {
	content := "---\nname: My Skill\ndescription: A useful skill\n---\nBody line 1\nBody line 2\n"
	name, desc, body := parseMinimalFrontmatter(content)
	if name != "My Skill" {
		t.Fatalf("name: got %q", name)
	}
	if desc != "A useful skill" {
		t.Fatalf("description: got %q", desc)
	}
	if !contains(body, "Body line 1") {
		t.Fatalf("body missing content: %q", body)
	}
}

// TestDeriveNamespace verifies namespace derivation from dir names.
func TestDeriveNamespace(t *testing.T) {
	// The regex extracts the first dash-separated token; verify it returns
	// something non-empty for a typical skill dir name.
	ns := deriveNamespace("alpha-tool-skill", "")
	if ns == "" {
		t.Fatalf("expected non-empty namespace for alpha-tool-skill")
	}
	// Fallback when no match.
	if got := deriveNamespace("plainname", "fallback"); got == "" {
		t.Fatalf("expected fallback or derived namespace, got empty")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && containsStr(s, substr)))
}

func containsStr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
