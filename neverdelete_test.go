package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const userMemory = "memory_1790000000000000001.md"

func skillDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		writeString(t, filepath.Join(dir, n, "SKILL.md"), "---\nname: "+n+"\ndescription: a skill\n---\nbody\n")
	}
	return dir
}

func has(path string) bool { _, err := os.Stat(path); return err == nil }

// Building a skill graph rebuilds its own files. It must never remove or
// replace anything else in the folder, whatever folder it is given.
func TestIngestNeverRemovesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, userMemory)
	custom := filepath.Join(dir, "memory_my-own-note.md")
	writeString(t, mine, "MINE\n")
	writeString(t, custom, "MINE too\n")
	if _, _, _, err := ingestMultiDir([]string{skillDir(t, "alpha", "beta")}, dir, nil); err != nil {
		t.Fatal(err)
	}
	// A second build drops what it generated and no longer needs.
	if _, _, _, err := ingestMultiDir([]string{skillDir(t, "alpha")}, dir, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{mine, custom} {
		if !has(path) || !strings.Contains(readString(t, path), "MINE") {
			t.Errorf("%s was removed or changed", filepath.Base(path))
		}
	}
	if !has(filepath.Join(dir, "memory_alpha.md")) || has(filepath.Join(dir, "memory_beta.md")) {
		t.Error("generated files were not rebuilt")
	}
}

// A graph that wrongly lists a user memory must not get it deleted.
func TestIngestKeepsUserMemoryListedInGraph(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, userMemory)
	writeString(t, mine, "MINE\n")
	writeString(t, filepath.Join(dir, "graph.json"), `{"version":1,"nodes":[{"id":"1790000000000000001"}],"edges":[]}`)
	if _, _, _, err := ingestMultiDir([]string{skillDir(t, "alpha")}, dir, nil); err != nil {
		t.Fatal(err)
	}
	if !has(mine) {
		t.Error("removed")
	}
}

func TestIngestRefusesToReplaceAnUnknownFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory_alpha.md")
	writeString(t, path, "MINE\n")
	if _, _, _, err := ingestMultiDir([]string{skillDir(t, "alpha")}, dir, nil); err == nil {
		t.Error("no error")
	}
	if got := readString(t, path); got != "MINE\n" {
		t.Errorf("replaced with %q", got)
	}
}

// What "serve" does on start, from inside a project.
func TestServeSyncLeavesProjectMemories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := memoryDir
	memoryDir = ""
	t.Cleanup(func() { memoryDir = old })
	project := t.TempDir()
	mine := filepath.Join(project, userMemory)
	writeString(t, mine, "MINE\n")
	s := &serverState{cfg: &Config{MemoryDir: project}, syncDirs: []string{skillDir(t, "alpha")}}
	if err := s.syncNow(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !has(mine) {
		t.Errorf("the project folder changed: %v", entries)
	}
	if len(s.graph.Nodes) == 0 {
		t.Error("serve has no skill graph")
	}
}

func TestInitKeepsMemoryIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MEMORY.md")
	writeString(t, path, "MINE\n")
	cfg := &Config{MemoryDir: dir, ScopeResolved: true}
	mcpInit(cfg, map[string]any{})
	if got := readString(t, path); got != "MINE\n" {
		t.Errorf("MCP init replaced it with %q", got)
	}
	handleInit(cfg)
	if got := readString(t, path); got != "MINE\n" {
		t.Errorf("init replaced it with %q", got)
	}
}

func TestSetupKeepsAnEditedSkillFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	path := filepath.Join(".devin", "skills", "memgraph-usage", "SKILL.md")
	writeString(t, path, "MINE\n")
	old := os.Args
	os.Args = []string{"memgraph", "setup"}
	t.Cleanup(func() { os.Args = old })
	handleSetup(&Config{MemoryDir: t.TempDir()})
	if got := readString(t, path); got != "MINE\n" {
		t.Errorf("replaced with %q", got)
	}
}

// A heading quoted in a code block is not the section.
func TestUpsertFileSectionIgnoresQuotedHeading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	original := "# Mine\n\n```md\n## Memgraph\nquoted example\n```\n\nkeep this\n"
	writeString(t, path, original)
	if err := upsertFileSection(path, "## Memgraph", "## Memgraph\n\nnew\n"); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, path); !strings.HasPrefix(got, original) || !strings.Contains(got, "\nnew\n") {
		t.Errorf("got:\n%s", got)
	}
}
