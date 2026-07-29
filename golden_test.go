package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// goldenCorpus builds a fixed set of SKILL.md files in a temp directory and
// returns the ingested GraphIndex + lookup map. The corpus is designed to
// exercise distinct ranking signals: exact name match, description substring,
// project-name boost, word-level IDF matching, and shared-keyword graph edges.
//
// Fixture skills (dir name -> project via deriveNamespace ^([a-z]+)-):
//
//	jwt-auth          -> jwt       "JSON Web Token authentication and signing"
//	token-validator   -> token     "Validate JWT tokens and check claims"
//	memory-toolbox    -> memory    "Persistent memory for AI agents with recall"
//	agent-bridge      -> agent     "Bridge AI agents to external tools and APIs"
//	docker-deploy     -> docker    "Deploy containers with Docker Compose"
//	log-parser        -> log       "Parse and search log files with regex"
func goldenCorpus(t *testing.T) (*GraphIndex, map[string]Memory) {
	t.Helper()
	srcDir := t.TempDir()

	skills := []struct {
		dir, name, desc, body string
	}{
		{"jwt-auth", "jwt-auth", "JSON Web Token authentication and signing", "jwt token signing for auth"},
		{"token-validator", "token-validator", "Validate JWT tokens and check claims", "token validation jwt claims"},
		{"memory-toolbox", "memory-toolbox", "Persistent memory for AI agents with recall", "agent memory storage recall"},
		{"agent-bridge", "agent-bridge", "Bridge AI agents to external tools and APIs", "agent integration bridge external"},
		{"docker-deploy", "docker-deploy", "Deploy containers with Docker Compose", "docker container deployment compose"},
		{"log-parser", "log-parser", "Parse and search log files with regex", "log parsing regex search"},
	}
	for _, s := range skills {
		dir := filepath.Join(srcDir, s.dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n%s\n", s.name, s.desc, s.body)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	targetDir := t.TempDir()
	cfg := &Config{MemoryDir: targetDir}
	if _, _, _, err := ingestMultiDir([]string{srcDir}, targetDir, cfg); err != nil {
		t.Fatalf("ingestMultiDir: %v", err)
	}
	graph, err := loadGraphIndex(targetDir)
	if err != nil {
		t.Fatalf("loadGraphIndex: %v", err)
	}
	lookup := buildLookupMap(graph)
	return graph, lookup
}

// TestGoldenCorpus_RankingOrder pins the top-N ordering for fixed queries
// against the golden corpus. This is the regression baseline: if a refactor or
// tuning change alters the ranking ORDER (not scores), this test fails.
//
// Scores are NOT pinned — only the order of result IDs. This allows legit
// weight tuning (scores change but order stays) while catching real ranking
// regressions (order changes).
func TestGoldenCorpus_RankingOrder(t *testing.T) {
	graph, lookup := goldenCorpus(t)

	tests := []struct {
		name           string
		query          string
		limit          int
		withGraphBoost bool
		// expectedOrder is the expected sequence of node IDs (skill nodes only,
		// namespace hubs are filtered out by rankNodes). Only the prefix up to
		// min(len(expectedOrder), limit) is checked.
		expectedOrder []string
	}{
		{
			name:           "jwt single word - name match dominates",
			query:          "jwt",
			limit:          5,
			withGraphBoost: false,
			// jwt-auth has "jwt" in name (+50 substring + 30*weight word);
			// token-validator has "jwt" only in description (+30 + 5*weight).
			expectedOrder: []string{"jwt-auth", "token-validator"},
		},
		{
			name:           "memory single word - name + project boost",
			query:          "memory",
			limit:          5,
			withGraphBoost: false,
			// memory-toolbox: name match + project="memory" (specific, >= 4 chars) boost.
			expectedOrder: []string{"memory-toolbox"},
		},
		{
			name:           "docker single word - name + project boost",
			query:          "docker",
			limit:          5,
			withGraphBoost: false,
			// docker-deploy: name match + project="docker" (specific) boost.
			expectedOrder: []string{"docker-deploy"},
		},
		{
			name:           "log single word - name match",
			query:          "log",
			limit:          5,
			withGraphBoost: false,
			// log-parser: name contains "log". project="log" is 3 chars, not
			// in specific3, so no project boost — but name match still dominates.
			expectedOrder: []string{"log-parser"},
		},
		{
			name:           "recommend jwt with graph boost",
			query:          "jwt",
			limit:          5,
			withGraphBoost: true,
			// Same order as without graph boost; jwt-auth and token-validator
			// share a "similar" or "shared-keyword" edge, boosting each other,
			// but the order shouldn't change since jwt-auth already leads.
			expectedOrder: []string{"jwt-auth", "token-validator"},
		},
		{
			name:           "parse logs multi-word",
			query:          "parse log",
			limit:          5,
			withGraphBoost: false,
			// log-parser: name="log-parser" matches "log" (word) + desc matches
			// "parse" and "log". No other skill mentions parsing or logs.
			expectedOrder: []string{"log-parser"},
		},
		{
			name:           "deploy containers multi-word",
			query:          "deploy containers",
			limit:          5,
			withGraphBoost: false,
			// docker-deploy: desc="Deploy containers with Docker Compose" has
			// both "deploy" and "containers". Name has "deploy".
			expectedOrder: []string{"docker-deploy"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items := rankNodes(graph, lookup, tc.query, tc.limit, tc.withGraphBoost)
			if len(items) == 0 {
				t.Fatalf("query %q returned 0 results, expected at least 1", tc.query)
			}
			checkLen := len(tc.expectedOrder)
			if tc.limit > 0 && tc.limit < checkLen {
				checkLen = tc.limit
			}
			if len(items) < checkLen {
				checkLen = len(items)
			}
			for i := 0; i < checkLen; i++ {
				if items[i].ID != tc.expectedOrder[i] {
					t.Errorf("query %q: position %d = %q, want %q\nfull results: %s",
						tc.query, i, items[i].ID, tc.expectedOrder[i], itemIDs(items))
					break
				}
			}
		})
	}
}

// TestGoldenCorpus_NoMatchReturnsEmpty verifies a query that matches nothing
// returns an empty result set, not nil-dereference or error.
func TestGoldenCorpus_NoMatchReturnsEmpty(t *testing.T) {
	graph, lookup := goldenCorpus(t)
	items := rankNodes(graph, lookup, "zzznomatchxyz", 10, false)
	if len(items) != 0 {
		t.Fatalf("expected 0 results for non-matching query, got %d: %s", len(items), itemIDs(items))
	}
}

// TestGoldenCorpus_NamespaceNodesFiltered verifies namespace hub nodes are
// never returned in ranking results.
func TestGoldenCorpus_NamespaceNodesFiltered(t *testing.T) {
	graph, lookup := goldenCorpus(t)
	items := rankNodes(graph, lookup, "memory", 100, false)
	for _, item := range items {
		if item.ID == "namespace_memory" || item.ID == "namespace_jwt" ||
			item.ID == "namespace_docker" || item.ID == "namespace_log" {
			t.Errorf("namespace hub %q should not appear in results", item.ID)
		}
	}
}

// itemIDs extracts the ordered list of IDs from QueryResultItems for error
// messages.
func itemIDs(items []QueryResultItem) string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return fmt.Sprintf("%v", ids)
}
