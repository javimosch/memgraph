package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// --- Issue #1 tests: stale graph, stemming, stop words ---

// TestContainsWord_BidirectionalStemming_TrackingMatchesTracker verifies that
// "tracking" (query word) matches "tracker" (text) via bidirectional stemming.
// Both stem to "track". This was the root cause of beads-tracker ranking below
// jar-portafolio-track for "local issue tracking" (issue #1).
func TestContainsWord_BidirectionalStemming_TrackingMatchesTracker(t *testing.T) {
	if !containsWord("beads-tracker", "tracking") {
		t.Fatal("containsWord('beads-tracker', 'tracking') should be true via bidirectional stemming")
	}
	if !containsWord("jar-portafolio-track", "tracking") {
		t.Fatal("containsWord('jar-portafolio-track', 'tracking') should be true via ing→stem")
	}
}

// TestContainsWord_BidirectionalStemming_ErSuffix verifies that "tracker" in
// text matches "track" query word via er-suffix stem on the text side.
func TestContainsWord_BidirectionalStemming_ErSuffix(t *testing.T) {
	if !containsWord("beads-tracker", "track") {
		t.Fatal("containsWord('beads-tracker', 'track') should be true via er-stem on text side")
	}
}

// TestContainsWord_NoFalsePositiveStemming verifies that short stems don't
// cause false positives. "er" stripping is only for words > 4 chars.
func TestContainsWord_NoFalsePositiveStemming(t *testing.T) {
	if containsWord("the", "there") {
		t.Fatal("containsWord('the', 'there') should be false — 'the' is too short for er-stem")
	}
}

// TestWordStems_Tracker verifies wordStems returns the base form.
func TestWordStems_Tracker(t *testing.T) {
	stems := wordStems("tracker")
	found := false
	for _, s := range stems {
		if s == "track" {
			found = true
		}
	}
	if !found {
		t.Fatalf("wordStems('tracker') should include 'track', got %v", stems)
	}
}

// TestWordStems_Tracking verifies wordStems strips ing suffix.
func TestWordStems_Tracking(t *testing.T) {
	stems := wordStems("tracking")
	found := false
	for _, s := range stems {
		if s == "track" {
			found = true
		}
	}
	if !found {
		t.Fatalf("wordStems('tracking') should include 'track', got %v", stems)
	}
}

// TestQueryStopWords_GenericActionVerbs verifies that generic action verbs
// are stop words so they don't inflate name-match scores (issue #1 P2).
func TestQueryStopWords_GenericActionVerbs(t *testing.T) {
	verbs := []string{"generate", "configure", "deploy", "install", "manage",
		"build", "run", "update", "delete", "remove", "check", "start", "stop",
		"monitor", "scan", "export", "import", "sync", "publish"}
	for _, v := range verbs {
		if !queryStopWords[v] {
			t.Fatalf("expected %q to be a stop word (generic action verb)", v)
		}
	}
}

// TestScoreNode_StopWordDoesNotScore verifies that a stop word like "generate"
// does not contribute to the score even if it appears in the skill name.
func TestScoreNode_StopWordDoesNotScore(t *testing.T) {
	node := Memory{
		ID:          "gen-vm",
		Name:        "generate-vm-access-prompt",
		Description: "Generate an AI prompt template for setting up SSH access",
		Project:     "infra",
		Content:     "",
	}
	idf := map[string]float64{"generate": 3.0, "changelog": 4.0}
	score := scoreNode(node, "generate changelog", idf)
	// "generate" is a stop word → no word-level bonus.
	// "changelog" is not in name/desc → no match.
	// Full-query substring: "generate changelog" is NOT a substring of name.
	// So score should be 0 (or very low from full-query desc match).
	if score > 10 {
		t.Fatalf("stop word 'generate' should not inflate score, got %f", score)
	}
}

// TestScoreNode_StemmingBoostsCorrectSkill verifies that "tracking" in the
// query matches "tracker" in the skill name via bidirectional stemming,
// giving beads-tracker a higher score than a skill without the stem match.
func TestScoreNode_StemmingBoostsCorrectSkill(t *testing.T) {
	beads := Memory{
		ID:          "beads-tracker",
		Name:        "beads-tracker",
		Description: "Use beads_rust as the default local issue tracker",
		Project:     "tools",
		Content:     "",
	}
	unrelated := Memory{
		ID:          "other",
		Name:        "other-skill",
		Description: "Completely unrelated to tracking or issues",
		Project:     "other",
		Content:     "",
	}
	idf := map[string]float64{"tracking": 2.0, "local": 1.0, "repo": 1.0}
	scoreBeads := scoreNode(beads, "local tracking repo", idf)
	scoreOther := scoreNode(unrelated, "local tracking repo", idf)
	if scoreBeads <= scoreOther {
		t.Fatalf("beads-tracker (%f) should outscore unrelated (%f) for 'tracking' query", scoreBeads, scoreOther)
	}
}

// TestScoreNode_WordOverlapSignal verifies that the word-overlap signal
// (learned from skills.match) boosts skills with more query words in
// name+description. A skill matching 3 query words should outscore one
// matching 1 query word, even if the 1-word match is in the name.
func TestScoreNode_WordOverlapSignal(t *testing.T) {
	// "multiple coding parallel" — 3 query words
	multiMatch := Memory{
		ID:          "mco",
		Name:        "supercli-mco",
		Description: "Orchestrate multiple AI coding agents in parallel using MCO",
		Project:     "supercli",
		Content:     "",
	}
	// "coding" only — 1 query word, but in the name
	singleMatch := Memory{
		ID:          "bridge",
		Name:        "coding-bridge-api",
		Description: "Provides access to the coding-bridge multi-provider AI system",
		Project:     "api",
		Content:     "",
	}
	idf := map[string]float64{"multiple": 2.0, "coding": 3.0, "parallel": 4.0}
	scoreMulti := scoreNode(multiMatch, "multiple coding parallel", idf)
	scoreSingle := scoreNode(singleMatch, "multiple coding parallel", idf)
	if scoreMulti <= scoreSingle {
		t.Fatalf("3-word overlap (%f) should outscore 1-word name match (%f)", scoreMulti, scoreSingle)
	}
}

// TestScanSkillFiles_FollowsSymlinks verifies that scanSkillFiles follows
// symlinked skill directories and deduplicates by realpath. This was a bug
// where ~50% of skills (all symlinked ones) were missing from the graph.
func TestScanSkillFiles_FollowsSymlinks(t *testing.T) {
	// Create a temp dir with a real skill and a symlinked skill
	tmpDir := t.TempDir()
	realDir := t.TempDir()

	// Real skill in the main dir
	realSkill := filepath.Join(tmpDir, "real-skill", "SKILL.md")
	os.MkdirAll(filepath.Dir(realSkill), 0755)
	os.WriteFile(realSkill, []byte("---\nname: real-skill\ndescription: A real skill\n---\n# real-skill\n"), 0644)

	// Symlinked skill: symlink in main dir → real dir
	symlinkedReal := filepath.Join(realDir, "symlinked-skill", "SKILL.md")
	os.MkdirAll(filepath.Dir(symlinkedReal), 0755)
	os.WriteFile(symlinkedReal, []byte("---\nname: symlinked-skill\ndescription: A symlinked skill\n---\n# symlinked-skill\n"), 0644)

	symlinkPath := filepath.Join(tmpDir, "symlinked-skill")
	os.Symlink(filepath.Join(realDir, "symlinked-skill"), symlinkPath)

	skills, err := scanSkillFiles(tmpDir)
	if err != nil {
		t.Fatalf("scanSkillFiles error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills (1 real + 1 symlinked), got %d: %+v", len(skills), skills)
	}
	ids := map[string]bool{}
	for _, s := range skills {
		ids[s.ID] = true
	}
	if !ids["real-skill"] {
		t.Fatal("missing real-skill")
	}
	if !ids["symlinked-skill"] {
		t.Fatal("missing symlinked-skill (symlink not followed)")
	}
}

// TestScanSkillFiles_DedupByRealpath verifies that the same skill appearing
// via multiple paths (e.g. ~/.agents + ~/.claude symlink) is only ingested once.
func TestScanSkillFiles_DedupByRealpath(t *testing.T) {
	realDir := t.TempDir()
	realSkill := filepath.Join(realDir, "shared-skill", "SKILL.md")
	os.MkdirAll(filepath.Dir(realSkill), 0755)
	os.WriteFile(realSkill, []byte("---\nname: shared-skill\ndescription: Shared\n---\n# shared\n"), 0644)

	// Two dirs, each with a symlink to the same real skill
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	os.Symlink(filepath.Join(realDir, "shared-skill"), filepath.Join(dir1, "shared-skill"))
	os.Symlink(filepath.Join(realDir, "shared-skill"), filepath.Join(dir2, "shared-skill"))

	skills1, _ := scanSkillFiles(dir1)
	skills2, _ := scanSkillFiles(dir2)
	// Each dir should find the skill once
	if len(skills1) != 1 || len(skills2) != 1 {
		t.Fatalf("expected 1 skill per dir, got %d and %d", len(skills1), len(skills2))
	}
}

// TestDiffFileState_CreatedModifiedDeleted verifies that diffFileState correctly
// detects created, modified, and deleted files between two scans.
func TestDiffFileState_CreatedModifiedDeleted(t *testing.T) {
	dir := t.TempDir()

	// Create three files
	p1 := filepath.Join(dir, "a.md")
	p2 := filepath.Join(dir, "b.md")
	p3 := filepath.Join(dir, "c.md")
	os.WriteFile(p1, []byte("a"), 0644)
	os.WriteFile(p2, []byte("b"), 0644)
	os.WriteFile(p3, []byte("c"), 0644)

	old := scanFileState([]string{dir})
	if len(old.mtimes) != 3 {
		t.Fatalf("expected 3 files, got %d", len(old.mtimes))
	}

	// Modify p1, delete p2, add p4
	time.Sleep(10 * time.Millisecond) // ensure mtime changes
	os.WriteFile(p1, []byte("a-modified"), 0644)
	os.Remove(p2)
	p4 := filepath.Join(dir, "d.md")
	os.WriteFile(p4, []byte("d"), 0644)

	new := scanFileState([]string{dir})
	changes := diffFileState(old, new)

	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d: %+v", len(changes), changes)
	}

	actions := map[string]string{}
	for _, c := range changes {
		actions[c.Path] = c.Action
	}
	if actions[p1] != "modified" {
		t.Errorf("p1 should be modified, got %s", actions[p1])
	}
	if actions[p2] != "deleted" {
		t.Errorf("p2 should be deleted, got %s", actions[p2])
	}
	if actions[p4] != "created" {
		t.Errorf("p4 should be created, got %s", actions[p4])
	}
}
