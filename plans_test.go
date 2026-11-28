package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// "graph-from-dir --include-plans" without a directory writes the global
// graph, which "plans" must find from any project.
func TestMCPPlansFindsTheGlobalGraph(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := memoryDir
	memoryDir = ""
	t.Cleanup(func() { memoryDir = old })
	writeString(t, filepath.Join(home, ".memgraph", "skills-graph", "graph.json"),
		`{"version":1,"nodes":[{"id":"p1","name":"My plan","type":"plan"}],"edges":[]}`)
	got := mcpPlans(&Config{MemoryDir: t.TempDir()})
	if !strings.Contains(got, "My plan") {
		t.Errorf("got %s", got)
	}
}
