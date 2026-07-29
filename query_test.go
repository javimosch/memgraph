package main

import (
	"testing"
)

// TestScoreNode_ExactNameMatch verifies the highest-weight scoring path: an
// exact name match contributes 100 to the score.
func TestScoreNode_ExactNameMatch(t *testing.T) {
	node := Memory{
		ID:          "jwt",
		Name:        "jwt",
		Description: "json web token helper",
		Project:     "auth",
		Content:     "jwt token signing",
	}
	idf := map[string]float64{"jwt": 1.0, "token": 0.5}
	score := scoreNode(node, "jwt", idf)
	if score < 100 {
		t.Fatalf("exact name match should score >= 100, got %f", score)
	}
}

// TestScoreNode_SubstringInDescription verifies a description substring match
// contributes the 30-point boost (query >= 3 chars).
func TestScoreNode_SubstringInDescription(t *testing.T) {
	node := Memory{
		ID:          "n",
		Name:        "unrelated name",
		Description: "a helper for proxmox lxc containers",
		Project:     "infra",
		Content:     "",
	}
	idf := map[string]float64{"proxmox": 1.0}
	score := scoreNode(node, "proxmox", idf)
	// Description contains "proxmox" -> +30. No name match, no project match.
	if score < 30 {
		t.Fatalf("description substring match should score >= 30, got %f", score)
	}
}

// TestScoreNode_ShortQueryNoSubstringMatch verifies that queries < 3 chars do
// NOT trigger the substring-based boosts (prevents "x" matching everything).
func TestScoreNode_ShortQueryNoSubstringMatch(t *testing.T) {
	node := Memory{
		ID:          "n",
		Name:        "extract tool",
		Description: "extract things from context",
		Project:     "p",
		Content:     "",
	}
	idf := map[string]float64{}
	score := scoreNode(node, "x", idf)
	// "x" is < 3 chars so no substring boost; word-level also skips < 3 chars.
	if score != 0 {
		t.Fatalf("short query 'x' should score 0, got %f", score)
	}
}

// TestSplitQueryWords_CamelCase verifies camelCase queries are split into
// lowercase words. All-caps runs (JWT, API) are kept together as one word.
func TestSplitQueryWords_CamelCase(t *testing.T) {
	// "addJwt" -> "add" + "jwt" (J is preceded by lowercase 'd', not all-caps).
	words := splitQueryWords("addJwt")
	got := map[string]bool{}
	for _, w := range words {
		got[w] = true
	}
	for _, want := range []string{"add", "jwt"} {
		if !got[want] {
			t.Fatalf("splitQueryWords(%q) missing %q, got %v", "addJwt", want, words)
		}
	}
}

// TestSplitQueryWords_AllCapsRunKeptTogether verifies that an all-caps acronym
// like "JWT" inside a query is NOT split into individual letters.
func TestSplitQueryWords_AllCapsRunKeptTogether(t *testing.T) {
	words := splitQueryWords("use JWT auth")
	got := map[string]bool{}
	for _, w := range words {
		got[w] = true
	}
	if !got["jwt"] {
		t.Fatalf("expected 'jwt' as a single word, got %v", words)
	}
}

// TestStripAccents verifies accented characters are reduced to ASCII.
func TestStripAccents(t *testing.T) {
	if got := stripAccents("café résumé"); got != "cafe resume" {
		t.Fatalf("stripAccents: got %q", got)
	}
	if got := stripAccents("naïve"); got != "naive" {
		t.Fatalf("stripAccents: got %q", got)
	}
}

// TestComputeIDF_ReturnsWeightsForQueryWords verifies IDF is computed for each
// query word present in the graph.
func TestComputeIDF_ReturnsWeightsForQueryWords(t *testing.T) {
	graph := &GraphIndex{
		Nodes: []Memory{
			{ID: "a", Name: "a", Description: "jwt token", Project: "p", Content: ""},
			{ID: "b", Name: "b", Description: "jwt signing", Project: "p", Content: ""},
			{ID: "c", Name: "c", Description: "unrelated", Project: "p", Content: ""},
		},
	}
	idf := computeIDF(graph, []string{"jwt", "token", "missing"})
	if _, ok := idf["jwt"]; !ok {
		t.Fatalf("expected idf entry for 'jwt', got %v", idf)
	}
	if _, ok := idf["token"]; !ok {
		t.Fatalf("expected idf entry for 'token', got %v", idf)
	}
	// 'missing' is not in the graph; computeIDF may or may not include it,
	// but the present ones must be there.
}

// TestBuildRelatedMap_Bidirectional verifies that an edge a->b shows up as a
// related edge for both a and b.
func TestBuildRelatedMap_Bidirectional(t *testing.T) {
	graph := &GraphIndex{
		Nodes: []Memory{{ID: "a"}, {ID: "b"}},
		Edges: []GraphEdge{{Source: "a", Target: "b", Relation: "similar", Value: "0.9"}},
	}
	rm := buildRelatedMap(graph)
	if len(rm["a"]) != 1 {
		t.Fatalf("expected 1 related edge for a, got %d", len(rm["a"]))
	}
	if len(rm["b"]) != 1 {
		t.Fatalf("expected 1 related edge for b (reverse), got %d", len(rm["b"]))
	}
}
