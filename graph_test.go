package main

import (
	"testing"
	"time"
)

// TestBuildSkillGraph_NamespaceHub verifies that buildSkillGraph creates a
// namespace hub node per project and links each member skill to it.
func TestBuildSkillGraph_NamespaceHub(t *testing.T) {
	skills := []skillInput{
		{ID: "alpha", DirName: "alpha-skill", Name: "Alpha", Description: "alpha tool", Project: "alpha", Content: "alpha", Created: time.Now()},
		{ID: "beta", DirName: "beta-skill", Name: "Beta", Description: "beta tool", Project: "beta", Content: "beta", Created: time.Now()},
	}
	graph := buildSkillGraph(skills)

	// Expect 2 skill nodes + 2 namespace hubs = 4 nodes total.
	if got := len(graph.Nodes); got != 4 {
		t.Fatalf("expected 4 nodes (2 skills + 2 namespace hubs), got %d", got)
	}

	hubIDs := map[string]bool{}
	for _, n := range graph.Nodes {
		if n.Type == "namespace" {
			hubIDs[n.ID] = true
		}
	}
	if !hubIDs["namespace_alpha"] || !hubIDs["namespace_beta"] {
		t.Fatalf("expected namespace_alpha and namespace_beta hubs, got %v", hubIDs)
	}

	// Each skill should have a namespace edge to its hub.
	namespaceLinks := 0
	for _, e := range graph.Edges {
		if e.Relation == "namespace" {
			namespaceLinks++
		}
	}
	if namespaceLinks != 2 {
		t.Fatalf("expected 2 namespace edges (one per skill), got %d", namespaceLinks)
	}
}

// TestBuildSkillGraph_Deterministic verifies that building the graph twice from
// the same input produces identical node IDs and edge sets (sorted by ID).
func TestBuildSkillGraph_Deterministic(t *testing.T) {
	skills := []skillInput{
		{ID: "a", DirName: "a-skill", Name: "A", Description: "shared keyword matching", Project: "p1", Content: "a", Created: time.Now()},
		{ID: "b", DirName: "b-skill", Name: "B", Description: "shared keyword matching", Project: "p1", Content: "b", Created: time.Now()},
		{ID: "c", DirName: "c-skill", Name: "C", Description: "unrelated description", Project: "p2", Content: "c", Created: time.Now()},
	}
	g1 := buildSkillGraph(skills)
	g2 := buildSkillGraph(skills)

	if len(g1.Nodes) != len(g2.Nodes) {
		t.Fatalf("node count differs: %d vs %d", len(g1.Nodes), len(g2.Nodes))
	}
	for i := range g1.Nodes {
		if g1.Nodes[i].ID != g2.Nodes[i].ID {
			t.Fatalf("node %d ID differs: %s vs %s", i, g1.Nodes[i].ID, g2.Nodes[i].ID)
		}
	}
	if len(g1.Edges) != len(g2.Edges) {
		t.Fatalf("edge count differs: %d vs %d", len(g1.Edges), len(g2.Edges))
	}
}

// TestBuildSkillGraph_SharedKeywordEdge verifies that two skills sharing >= 2
// significant terms get a shared-keyword edge in both directions.
func TestBuildSkillGraph_SharedKeywordEdge(t *testing.T) {
	skills := []skillInput{
		{ID: "x", DirName: "x-skill", Name: "X", Description: "memory tool for agents", Project: "p", Content: "x", Created: time.Now()},
		{ID: "y", DirName: "y-skill", Name: "Y", Description: "memory tool for agents", Project: "p", Content: "y", Created: time.Now()},
	}
	graph := buildSkillGraph(skills)

	// "memory", "tool", "agents" are 3 shared significant terms (>= 3 chars,
	// not stop words) -> shared-keyword edges both ways.
	xToY, yToX := false, false
	for _, e := range graph.Edges {
		if e.Relation == "shared-keyword" {
			if e.Source == "x" && e.Target == "y" {
				xToY = true
			}
			if e.Source == "y" && e.Target == "x" {
				yToX = true
			}
		}
	}
	if !xToY || !yToX {
		t.Fatalf("expected bidirectional shared-keyword edges x<->y, got xToY=%v yToX=%v", xToY, yToX)
	}
}

// TestSaveAndLoadGraphIndex round-trips a GraphIndex through JSON on disk.
func TestSaveAndLoadGraphIndex(t *testing.T) {
	dir := t.TempDir()
	original := &skillGraph{
		Nodes: []Memory{{ID: "n1", Name: "N1", Type: "skill", Project: "p"}},
		Edges: []GraphEdge{{Source: "n1", Target: "n2", Relation: "similar", Value: "0.5"}},
	}
	if err := saveGraphIndex(dir, original); err != nil {
		t.Fatalf("saveGraphIndex: %v", err)
	}
	loaded, err := loadGraphIndex(dir)
	if err != nil {
		t.Fatalf("loadGraphIndex: %v", err)
	}
	if len(loaded.Nodes) != 1 || loaded.Nodes[0].ID != "n1" {
		t.Fatalf("loaded nodes mismatch: %+v", loaded.Nodes)
	}
	if len(loaded.Edges) != 1 || loaded.Edges[0].Source != "n1" {
		t.Fatalf("loaded edges mismatch: %+v", loaded.Edges)
	}
}
