package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// QueryResult is the agent-facing JSON output for `memgraph query`
type QueryResult struct {
	Query   string        `json:"query"`
	Count   int           `json:"count"`
	Results []QueryResultItem `json:"results"`
}

type QueryResultItem struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	FilePath    string   `json:"file_path"`
	Score       float64  `json:"score"`
	Tags        []string `json:"tags"`
	Related     []RelatedNode `json:"related,omitempty"`
}

type RelatedNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Relation    string `json:"relation"`
	Project     string `json:"project"`
	FilePath    string `json:"file_path"`
}

// RelatedResult is the output for `memgraph related`
type RelatedResult struct {
	Skill   QueryResultItem `json:"skill"`
	Count   int             `json:"count"`
	Related []RelatedNode    `json:"related"`
}

// RecommendResult is the output for `memgraph recommend`
type RecommendResult struct {
	Task      string             `json:"task"`
	Count     int                `json:"count"`
	Recommended []QueryResultItem `json:"recommended"`
}

func loadGraphForQuery(cfg *Config) (*GraphIndex, map[string]Memory, error) {
	// Try the configured memory dir first
	candidates := []string{cfg.MemoryDir}

	// Add the global ~/.memgraph/skills-graph path
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".memgraph", "skills-graph"),
			filepath.Join(home, ".sick-memory", "skills-graph"),
		)
		// Also scan ~/.memgraph/*/graph.json for any graph dirs
		globalDir := filepath.Join(home, ".memgraph")
		if entries, err := os.ReadDir(globalDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					candidates = append(candidates, filepath.Join(globalDir, e.Name()))
				}
			}
		}
	}

	for _, path := range candidates {
		graphFile := filepath.Join(path, "graph.json")
		if _, err := os.Stat(graphFile); err == nil {
			graph, err := loadGraphIndex(path)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to load graph from %s: %w", path, err)
			}
			lookup := make(map[string]Memory, len(graph.Nodes))
			for _, n := range graph.Nodes {
				lookup[n.ID] = n
			}
			return graph, lookup, nil
		}
	}

	return nil, nil, fmt.Errorf("no graph found — run 'memgraph serve --sync-dir <dir>' or 'memgraph graph-from-dir <dir>' first")
}

func buildRelatedMap(graph *GraphIndex) map[string][]GraphEdge {
	related := make(map[string][]GraphEdge)
	for _, e := range graph.Edges {
		related[e.Source] = append(related[e.Source], e)
		related[e.Target] = append(related[e.Target], GraphEdge{Source: e.Target, Target: e.Source, Relation: e.Relation, Value: e.Value})
	}
	return related
}

func nodeToRelatedNode(node Memory, relation string) RelatedNode {
	return RelatedNode{
		ID:       node.ID,
		Name:     node.Name,
		Relation: relation,
		Project:  node.Project,
		FilePath: node.FilePath,
	}
}

func getRelatedForNode(nodeID string, relatedMap map[string][]GraphEdge, lookup map[string]Memory) []RelatedNode {
	edges := relatedMap[nodeID]
	seen := make(map[string]bool)
	var related []RelatedNode
	for _, e := range edges {
		otherID := e.Target
		if otherID == nodeID {
			otherID = e.Source
		}
		if seen[otherID] {
			continue
		}
		seen[otherID] = true
		if n, ok := lookup[otherID]; ok {
			related = append(related, nodeToRelatedNode(n, e.Relation))
		}
	}
	return related
}

var queryStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "to": true, "of": true, "in": true,
	"for": true, "on": true, "at": true, "by": true, "with": true, "from": true,
	"add": true, "new": true, "how": true, "what": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"this": true, "that": true, "these": true, "those": true,
	"it": true, "its": true, "i": true, "we": true, "you": true,
	"do": true, "does": true, "did": true, "can": true, "could": true,
	"will": true, "would": true, "should": true, "shall": true,
	"and": true, "or": true, "but": true, "not": true, "no": true,
	"called": true, "named": true, "about": true, "into": true,
	"my": true, "me": true, "use": true, "using": true, "setup": true,
	"set": true, "get": true, "make": true, "create": true,
}

func scoreNode(node Memory, query string) float64 {
	q := strings.ToLower(query)
	name := strings.ToLower(node.Name)
	desc := strings.ToLower(node.Description)
	project := strings.ToLower(node.Project)

	var score float64
	// Exact name match
	if name == q {
		score += 100
	}
	// Name contains query
	if strings.Contains(name, q) {
		score += 50
	}
	// Description contains query
	if strings.Contains(desc, q) {
		score += 30
	}
	// Project contains query
	if strings.Contains(project, q) {
		score += 20
	}
	// Word-level matches (skip stop words)
	queryWords := strings.Fields(q)
	for _, qw := range queryWords {
		if len(qw) < 2 || queryStopWords[qw] {
			continue
		}
		if strings.Contains(name, qw) {
			score += 15
		}
		if strings.Contains(desc, qw) {
			score += 8
		}
		if strings.Contains(project, qw) {
			score += 5
		}
		for _, tag := range node.Tags {
			if strings.Contains(strings.ToLower(tag), qw) {
				score += 10
			}
		}
	}
	return score
}

func handleQuery(cfg *Config) {
	args := os.Args[2:]
	var queryParts []string
	var limit int = 10

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit", "-l":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &limit)
				i++
			}
		case "--json", "-j":
			jsonOutput = true
		case "--memory-dir":
			if i+1 < len(args) {
				i++
			}
		default:
			if !strings.HasPrefix(args[i], "--memory-dir=") && !strings.HasPrefix(args[i], "--") {
				queryParts = append(queryParts, args[i])
			}
		}
	}
	query := strings.Join(queryParts, " ")

	if query == "" {
		fmt.Fprintln(os.Stderr, "Usage: memgraph query <text> [--limit N] [--json]")
		os.Exit(85)
	}

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(92)
	}

	relatedMap := buildRelatedMap(graph)

	type scored struct {
		node  Memory
		score float64
	}
	var results []scored
	for _, n := range graph.Nodes {
		if n.Type == "namespace" {
			continue
		}
		s := scoreNode(n, query)
		if s > 0 {
			results = append(results, scored{node: n, score: s})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].node.Name < results[j].node.Name
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	var items []QueryResultItem
	for _, r := range results {
		item := QueryResultItem{
			ID:          r.node.ID,
			Name:        r.node.Name,
			Description: r.node.Description,
			Project:     r.node.Project,
			FilePath:    r.node.FilePath,
			Score:       r.score,
			Tags:        r.node.Tags,
			Related:     getRelatedForNode(r.node.ID, relatedMap, lookup),
		}
		items = append(items, item)
	}

	result := QueryResult{
		Query:   query,
		Count:   len(items),
		Results: items,
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("Query: %s  (%d results)\n\n", query, result.Count)
		for _, item := range items {
			fmt.Printf("  %s  [score: %.0f]\n", item.Name, item.Score)
			fmt.Printf("  %s\n", item.Description)
			if item.FilePath != "" {
				fmt.Printf("  path: %s\n", item.FilePath)
			}
			if len(item.Related) > 0 {
				names := make([]string, 0, len(item.Related))
				for _, r := range item.Related {
					names = append(names, r.Name+" ("+r.Relation+")")
				}
				fmt.Printf("  related: %s\n", strings.Join(names, ", "))
			}
			fmt.Println()
		}
	}
}

func handleRelated(cfg *Config) {
	args := os.Args[2:]
	var target string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "-j":
			jsonOutput = true
		case "--memory-dir":
			if i+1 < len(args) {
				i++
			}
		default:
			if target == "" && !strings.HasPrefix(args[i], "--memory-dir=") && !strings.HasPrefix(args[i], "--") {
				target = args[i]
			}
		}
	}

	if target == "" {
		fmt.Fprintln(os.Stderr, "Usage: memgraph related <skill-id-or-name> [--json]")
		os.Exit(85)
	}

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(92)
	}

	// Find node by ID or name (case-insensitive)
	var node Memory
	var found bool
	targetLower := strings.ToLower(target)
	for _, n := range graph.Nodes {
		if n.ID == target || strings.ToLower(n.Name) == targetLower {
			node = n
			found = true
			break
		}
	}
	if !found {
		// Partial match
		for _, n := range graph.Nodes {
			if strings.Contains(strings.ToLower(n.Name), targetLower) {
				node = n
				found = true
				break
			}
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "Skill not found: %s\n", target)
		os.Exit(92)
	}

	relatedMap := buildRelatedMap(graph)
	related := getRelatedForNode(node.ID, relatedMap, lookup)

	// Sort by relation priority
	relationOrder := map[string]int{"references": 0, "similar": 1, "shared-keyword": 2, "namespace": 3}
	sort.Slice(related, func(i, j int) bool {
		ri, rj := relationOrder[related[i].Relation], relationOrder[related[j].Relation]
		if ri != rj {
			return ri < rj
		}
		return related[i].Name < related[j].Name
	})

	result := RelatedResult{
		Skill: QueryResultItem{
			ID:          node.ID,
			Name:        node.Name,
			Description: node.Description,
			Project:     node.Project,
			FilePath:    node.FilePath,
			Tags:        node.Tags,
		},
		Count:   len(related),
		Related: related,
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("Skill: %s\n", node.Name)
		fmt.Printf("  %s\n", node.Description)
		if node.FilePath != "" {
			fmt.Printf("  path: %s\n", node.FilePath)
		}
		fmt.Printf("\nRelated (%d):\n", result.Count)
		for _, r := range related {
			fmt.Printf("  [%s] %s", r.Relation, r.Name)
			if r.Project != "" {
				fmt.Printf("  (%s)", r.Project)
			}
			fmt.Println()
		}
	}
}

func handleRecommend(cfg *Config) {
	args := os.Args[2:]
	var taskParts []string
	var limit int = 5

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit", "-l":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &limit)
				i++
			}
		case "--json", "-j":
			jsonOutput = true
		case "--memory-dir":
			if i+1 < len(args) {
				i++
			}
		default:
			if !strings.HasPrefix(args[i], "--memory-dir=") && !strings.HasPrefix(args[i], "--") {
				taskParts = append(taskParts, args[i])
			}
		}
	}
	task := strings.Join(taskParts, " ")

	if task == "" {
		fmt.Fprintln(os.Stderr, "Usage: memgraph recommend <task description> [--limit N] [--json]")
		os.Exit(85)
	}

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(92)
	}

	relatedMap := buildRelatedMap(graph)

	// Score all skill nodes
	type scored struct {
		node  Memory
		score float64
	}
	var results []scored
	for _, n := range graph.Nodes {
		if n.Type == "namespace" {
			continue
		}
		s := scoreNode(n, task)
		if s > 0 {
			results = append(results, scored{node: n, score: s})
		}
	}

	// Boost nodes that are connected to other high-scoring nodes
	scoreByID := make(map[string]float64)
	for _, r := range results {
		scoreByID[r.node.ID] = r.score
	}
	for i, r := range results {
		edges := relatedMap[r.node.ID]
		for _, e := range edges {
			otherID := e.Target
			if otherID == r.node.ID {
				otherID = e.Source
			}
			if otherScore, ok := scoreByID[otherID]; ok {
				results[i].score += otherScore * 0.05
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].node.Name < results[j].node.Name
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	var items []QueryResultItem
	for _, r := range results {
		item := QueryResultItem{
			ID:          r.node.ID,
			Name:        r.node.Name,
			Description: r.node.Description,
			Project:     r.node.Project,
			FilePath:    r.node.FilePath,
			Score:       r.score,
			Tags:        r.node.Tags,
			Related:     getRelatedForNode(r.node.ID, relatedMap, lookup),
		}
		items = append(items, item)
	}

	result := RecommendResult{
		Task:        task,
		Count:       len(items),
		Recommended: items,
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("Task: %s\n", task)
		fmt.Printf("Recommended skills (%d):\n\n", result.Count)
		for _, item := range items {
			fmt.Printf("  %s  [score: %.0f]\n", item.Name, item.Score)
			fmt.Printf("  %s\n", item.Description)
			if item.FilePath != "" {
				fmt.Printf("  path: %s\n", item.FilePath)
			}
			fmt.Println()
		}
		if len(items) == 0 {
			fmt.Println("  No matching skills found. Try broader terms.")
		}
	}
}
