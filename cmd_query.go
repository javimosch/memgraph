package main

import (
	"encoding/json"
	"fmt"
	"math"
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
	var candidates []string

	// If memoryDir was explicitly set via --memory-dir, try it FIRST
	// (before the global graph) so the user's override is respected.
	if memoryDir != "" {
		candidates = append(candidates, memoryDir)
	}

	// Prefer the global ~/.memgraph/skills-graph path (the canonical skill graph)
	// over project-specific memory dirs, which may have stale graphs.
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".memgraph", "skills-graph"),
			filepath.Join(home, ".sick-memory", "skills-graph"),
		)
		// Also scan ~/.memgraph/*/graph.json for any graph dirs
		globalDir := filepath.Join(home, ".memgraph")
		if entries, err := os.ReadDir(globalDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && e.Name() != "skills-graph" {
					candidates = append(candidates, filepath.Join(globalDir, e.Name()))
				}
			}
		}
	}

	// Add the configured memory dir as a fallback (if not already added)
	if memoryDir == "" {
		candidates = append(candidates, cfg.MemoryDir)
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

func buildLookupMap(graph *GraphIndex) map[string]Memory {
	lookup := make(map[string]Memory, len(graph.Nodes))
	for _, n := range graph.Nodes {
		lookup[n.ID] = n
	}
	return lookup
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

// relationPriority ranks relation types — lower = more relevant.
var relationPriority = map[string]int{
	"similar":       0,
	"shared-keyword": 1,
	"references":    2,
	"namespace":     3,
}

func getRelatedForNode(nodeID string, relatedMap map[string][]GraphEdge, lookup map[string]Memory) []RelatedNode {
	return getRelatedForNodeLimit(nodeID, relatedMap, lookup, 0)
}

// getRelatedForNodeLimit returns at most maxRelated related nodes (0 = no limit).
// Namespace nodes and sub-file references are filtered out — agents only need
// real skill nodes. Results are sorted by relation priority (similar first).
func getRelatedForNodeLimit(nodeID string, relatedMap map[string][]GraphEdge, lookup map[string]Memory, maxRelated int) []RelatedNode {
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
		n, ok := lookup[otherID]
		if !ok {
			continue
		}
		// Filter out namespace nodes — they're not real skills
		if n.Type == "namespace" {
			continue
		}
		// Filter out sub-file references — nodes that are .md files inside a
		// skill directory but aren't the skill's main file (SKILL.md or the
		// dir-name.md). We detect this by checking if the filename is a common
		// sub-file name (README, references/*, etc.) OR if there's a SKILL.md
		// in the same directory (meaning this node is a sub-file of that skill).
		if n.FilePath == "" {
			continue
		}
		if isSubFileReference(n.FilePath, lookup) {
			continue
		}
		related = append(related, nodeToRelatedNode(n, e.Relation))
	}
	// Sort by relation priority (similar < shared-keyword < references < namespace)
	sort.SliceStable(related, func(i, j int) bool {
		pi, pj := 99, 99
		if v, ok := relationPriority[related[i].Relation]; ok {
			pi = v
		}
		if v, ok := relationPriority[related[j].Relation]; ok {
			pj = v
		}
		return pi < pj
	})
	if maxRelated > 0 && len(related) > maxRelated {
		related = related[:maxRelated]
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
	"server": true, "system": true, "app": true, "application": true,
	"tool": true, "cli": true, "config": true, "help": true,
	"need": true, "want": true, "like": true, "also": true,
	"fix": true, "issue": true, "problem": true, "debug": true,
	"error": true, "wrong": true, "broken": true, "fail": true,
	"skill": true, "skills": true, "agent": true, "agents": true,
	"git": true, // "git" is too common (many skills mention git); "GitHub" splits to "git"+"hub"
}

func scoreNode(node Memory, query string, idf map[string]float64) float64 {
	q := stripAccents(strings.ToLower(query))
	name := stripAccents(strings.ToLower(node.Name))
	desc := stripAccents(strings.ToLower(node.Description))
	project := stripAccents(strings.ToLower(node.Project))
	// Extended description: first 1000 chars of content, for deeper keyword matching
	// This catches terms like "proxmox", "LXC", "exit 132" that are in the skill body
	// but not in the short frontmatter description.
	extendedDesc := ""
	if len(node.Content) > 0 {
		extended := node.Content
		if len(extended) > 1000 {
			extended = extended[:1000]
		}
		extendedDesc = stripAccents(strings.ToLower(extended))
	}

	var score float64
	// Full-query substring matches only for queries >= 3 chars
	// (prevents single chars like "x" matching "extract", "context", etc.)
	if len(q) >= 3 {
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
	}
	// Project name boost: if the query contains the skill's project name as a
	// whole word, this skill is likely about that project. E.g., "add JWT to tau"
	// should boost tau-maintenance even if "JWT" matches simpliciti-apiv3-jwt.
	// Only apply for specific project names (>= 4 chars or contains non-alpha),
	// to avoid generic projects like "agent", "jar", "am" getting boosted.
	if project != "" && len(project) >= 3 && !queryStopWords[project] {
		if isSpecificProjectName(project) {
			if containsWord(q, project) {
				score += 30
			}
		}
	}
	// Word-level matches (skip stop words, use word-boundary matching, IDF-weighted)
	// Note: splitQueryWords must be called on the original query (before lowercasing)
	// so that camelCase splitting works. The function already lowercases the output.
	queryWords := splitQueryWords(query)
	for _, qw := range queryWords {
		if len(qw) < 3 {
			continue
		}
		// Skip pure numbers (port numbers, version numbers — not useful for skill search)
		if isAllDigits(qw) {
			continue
		}
		// Check stop words on both the word and its singular form
		if queryStopWords[qw] {
			continue
		}
		if strings.HasSuffix(qw, "s") && len(qw) > 3 && queryStopWords[qw[:len(qw)-1]] {
			continue
		}
		if strings.HasSuffix(qw, "ing") && len(qw) > 4 && queryStopWords[qw[:len(qw)-3]] {
			continue
		}
		// IDF weight: rare terms score higher, common terms score lower
		weight := idf[qw]
		if weight == 0 {
			weight = 1.0
		}
		if containsWord(name, qw) {
			score += 30 * weight
		}
		if containsWord(desc, qw) {
			score += 5 * weight
		}
		if containsWord(extendedDesc, qw) {
			score += 3 * weight
		}
		if containsWord(project, qw) {
			score += 5 * weight
		}
		for _, tag := range node.Tags {
			if containsWord(strings.ToLower(tag), qw) {
				score += 10 * weight
			}
		}
	}

	// All-words-match bonus: if every non-stopword query word matches this node
	// (in name, desc, extended, project, or tags), give a bonus. This helps
	// multi-word concept queries like "skill discovery" match skills that
	// contain both words, rather than skills that strongly match just one word.
	if len(queryWords) > 1 {
		allMatch := true
		anyMatch := false
		for _, qw := range queryWords {
			if len(qw) < 3 || isAllDigits(qw) || queryStopWords[qw] {
				continue
			}
			matched := containsWord(name, qw) || containsWord(desc, qw) ||
				containsWord(extendedDesc, qw) || containsWord(project, qw)
			if !matched {
				for _, tag := range node.Tags {
					if containsWord(strings.ToLower(tag), qw) {
						matched = true
						break
					}
				}
			}
			if matched {
				anyMatch = true
			} else {
				allMatch = false
			}
		}
		if allMatch && anyMatch {
			score += 50
		}
	}

	return score
}

// computeIDF calculates inverse document frequency for each query word.
// Words that appear in many skills get a low weight; rare words get a high weight.
func computeIDF(graph *GraphIndex, queryWords []string) map[string]float64 {
	totalDocs := len(graph.Nodes)
	if totalDocs == 0 {
		totalDocs = 1
	}
	// Normalize query words with accent stripping
	normalizedWords := make([]string, 0, len(queryWords))
	for _, qw := range queryWords {
		normalizedWords = append(normalizedWords, stripAccents(strings.ToLower(qw)))
	}

	docFreq := make(map[string]int)
	for _, n := range graph.Nodes {
		if n.Type == "namespace" {
			continue
		}
		name := stripAccents(strings.ToLower(n.Name))
		desc := stripAccents(strings.ToLower(n.Description))
		project := stripAccents(strings.ToLower(n.Project))
		extended := ""
		if len(n.Content) > 0 {
			ext := n.Content
			if len(ext) > 1000 {
				ext = ext[:1000]
			}
			extended = stripAccents(strings.ToLower(ext))
		}
		combined := name + " " + desc + " " + project + " " + extended
		for _, qw := range normalizedWords {
			if len(qw) < 3 {
				continue
			}
			if queryStopWords[qw] {
				continue
			}
			if strings.HasSuffix(qw, "s") && len(qw) > 3 && queryStopWords[qw[:len(qw)-1]] {
				continue
			}
			if strings.HasSuffix(qw, "ing") && len(qw) > 4 && queryStopWords[qw[:len(qw)-3]] {
				continue
			}
			if containsWord(combined, qw) {
				docFreq[qw]++
			}
		}
	}
	idf := make(map[string]float64)
	for _, qw := range normalizedWords {
		if len(qw) < 3 {
			continue
		}
		if queryStopWords[qw] {
			continue
		}
		if strings.HasSuffix(qw, "s") && len(qw) > 3 && queryStopWords[qw[:len(qw)-1]] {
			continue
		}
		if strings.HasSuffix(qw, "ing") && len(qw) > 4 && queryStopWords[qw[:len(qw)-3]] {
			continue
		}
		df := docFreq[qw]
		if df == 0 {
			idf[qw] = 3.0 // rare term not found in any skill
		} else {
			// Standard IDF: log(N / df), clamped to [0.2, 5.0]
			idf[qw] = math.Log(float64(totalDocs) / float64(df))
			if idf[qw] < 0.2 {
				idf[qw] = 0.2
			}
			if idf[qw] > 5.0 {
				idf[qw] = 5.0
			}
		}
	}
	return idf
}

// containsWord checks if text contains word as a whole word (boundary match).
// Also handles simple stemming: "backups" matches "backup", "deploying" matches "deploy".
func containsWord(text, word string) bool {
	if text == word {
		return true
	}
	// Try exact word boundary match first
	if hasWordBoundary(text, word) {
		return true
	}
	// Simple stemming: strip trailing "s" (plural → singular)
	if strings.HasSuffix(word, "s") && len(word) > 3 {
		singular := word[:len(word)-1]
		if hasWordBoundary(text, singular) {
			return true
		}
	}
	// Simple stemming: strip trailing "ing"
	if strings.HasSuffix(word, "ing") && len(word) > 4 {
		base := word[:len(word)-3]
		if hasWordBoundary(text, base) {
			return true
		}
	}
	// Prefix match for 4+ char words: "mongo" matches "mongodb", "mongodb" matches "mongo"
	// Bidirectional but with constraints to prevent false positives like
	// "rbm2" matching "rbm21" (different containers).
	if len(word) >= 4 {
		words := strings.FieldsFunc(text, func(c rune) bool {
			return c == ' ' || c == '-' || c == '_' || c == '.' || c == ',' || c == '/' || c == '(' || c == ')'
		})
		for _, w := range words {
			if w == word {
				return true
			}
			// Query word is prefix of text word: "mongo" matches "mongodb"
			if strings.HasPrefix(w, word) && len(w) <= len(word)+4 {
				return true
			}
			// Text word is prefix of query word: "mongodb" matches "mongo"
			// Only if text word is at least 5 chars (prevents "rbm2" matching "rbm21")
			if strings.HasPrefix(word, w) && len(w) >= 5 && len(word) <= len(w)+4 {
				return true
			}
		}
	}
	// For 3-char words, use exact match only (handled by hasWordBoundary above)
	return false
}

func hasWordBoundary(text, word string) bool {
	if text == word {
		return true
	}
	idx := strings.Index(text, word)
	for idx >= 0 {
		end := idx + len(word)
		leftOK := idx == 0 || !isWordChar(text[idx-1])
		rightOK := end == len(text) || !isWordChar(text[end])
		if leftOK && rightOK {
			return true
		}
		next := idx + 1
		if next >= len(text) {
			break
		}
		idx = strings.Index(text[next:], word)
		if idx < 0 {
			break
		}
		idx = next + idx
	}
	return false
}

func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// splitQueryWords splits a query string into words, handling camelCase,
// dots, slashes, underscores, and hyphens as word boundaries.
// E.g. "a2aUseCase" → ["a2a", "use", "case"], "jar/rbm21/manage" → ["jar", "rbm21", "manage"]
// All-caps words like "JWT" or "API" are kept as whole words, not split per-letter.
// Accents are stripped from each word for accent-insensitive matching.
func splitQueryWords(s string) []string {
	// First, insert spaces before uppercase letters (camelCase split)
	// but don't split all-caps words (JWT, API, SSL, etc.)
	var sb strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c >= 'A' && c <= 'Z' {
			// Check if this is part of an all-caps run (2+ consecutive uppercase)
			isAllCaps := false
			if i > 0 && runes[i-1] >= 'A' && runes[i-1] <= 'Z' {
				isAllCaps = true
			}
			if i+1 < len(runes) && runes[i+1] >= 'A' && runes[i+1] <= 'Z' {
				isAllCaps = true
			}
			if !isAllCaps && i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteRune(c + 32) // lowercase
		} else {
			sb.WriteRune(c)
		}
	}
	// Now split on whitespace, hyphens, underscores, dots, slashes, etc.
	words := strings.FieldsFunc(sb.String(), func(c rune) bool {
		return c == ' ' || c == '-' || c == '_' || c == '.' || c == '/' || c == '\\' || c == ',' || c == ';' || c == ':' || c == '(' || c == ')'
	})
	// Strip accents from each word for accent-insensitive matching
	for i, w := range words {
		words[i] = stripAccents(w)
	}
	return words
}

// isSpecificProjectName returns true for project names that are specific enough
// to boost. Generic single-word projects like "agent", "jar", "am", "general"
// return false. Specific projects like "tau", "pve2", "supercli", "coolify"
// return true.
func isSpecificProjectName(project string) bool {
	// Generic project names that should never get a boost
	generic := map[string]bool{
		"agent": true, "general": true, "jar": true, "am": true,
		"add": true, "chat": true, "find": true, "audit": true,
		"cool": true, "extract": true, "global": true, "token": true,
		"project": true, "mr": true, "pi": true, "rbm": true,
		"deployment": true, "smoke": true, "context": true,
	}
	if generic[project] {
		return false
	}
	// Projects with numbers are specific (pve2, rbm21, dk2)
	for i := 0; i < len(project); i++ {
		if project[i] >= '0' && project[i] <= '9' {
			return true
		}
	}
	// Projects >= 4 chars are specific enough
	if len(project) >= 4 {
		return true
	}
	// 3-char projects: only boost if they're known specific names
	specific3 := map[string]bool{
		"tau": true, "mem": true, "mco": true, "a2a": true,
	}
	return specific3[project]
}

// stripAccents removes diacritical marks from common Latin characters.
// Handles French, Spanish, German, Portuguese, Italian accents.
func stripAccents(s string) string {
	// Fast path: if no non-ASCII bytes, return as-is
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	// Common accent replacements (UTF-8 sequences → ASCII)
	replacements := []struct{ from, to string }{
		{"à", "a"}, {"á", "a"}, {"â", "a"}, {"ã", "a"}, {"ä", "a"}, {"å", "a"},
		{"ç", "c"},
		{"è", "e"}, {"é", "e"}, {"ê", "e"}, {"ë", "e"},
		{"ì", "i"}, {"í", "i"}, {"î", "i"}, {"ï", "i"},
		{"ñ", "n"},
		{"ò", "o"}, {"ó", "o"}, {"ô", "o"}, {"õ", "o"}, {"ö", "o"},
		{"ù", "u"}, {"ú", "u"}, {"û", "u"}, {"ü", "u"},
		{"ý", "y"}, {"ÿ", "y"},
		{"À", "A"}, {"Á", "A"}, {"Â", "A"}, {"Ã", "A"}, {"Ä", "A"}, {"Å", "A"},
		{"Ç", "C"},
		{"È", "E"}, {"É", "E"}, {"Ê", "E"}, {"Ë", "E"},
		{"Ì", "I"}, {"Í", "I"}, {"Î", "I"}, {"Ï", "I"},
		{"Ñ", "N"},
		{"Ò", "O"}, {"Ó", "O"}, {"Ô", "O"}, {"Õ", "O"}, {"Ö", "O"},
		{"Ù", "U"}, {"Ú", "U"}, {"Û", "U"}, {"Ü", "U"},
		{"Ý", "Y"},
		{"œ", "oe"}, {"Œ", "OE"}, {"æ", "ae"}, {"Æ", "AE"},
		{"ß", "ss"},
	}
	r := s
	for _, rep := range replacements {
		r = strings.ReplaceAll(r, rep.from, rep.to)
	}
	return r
}

// isSubFileReference checks if a file path is a sub-file of a skill directory
// (e.g., README.md, references/dev.md) rather than the skill's main file.
func isSubFileReference(filePath string, lookup map[string]Memory) bool {
	lower := strings.ToLower(filePath)
	base := filepath.Base(lower)
	// The main skill file is never a sub-file
	if base == "skill.md" {
		return false
	}
	// A standalone .md file in the skills root (like agent-exchange.md) is a
	// real skill, not a sub-file — it has no directory/skill.md sibling.
	// Detect: path is .../skills/<name>.md (no intermediate directory)
	parts := strings.Split(strings.TrimSuffix(lower, "/"), "/")
	if len(parts) >= 2 && parts[len(parts)-2] == "skills" && base == strings.ToLower(parts[len(parts)-1]) {
		return false
	}
	// README.md is always a sub-file
	if base == "readme.md" {
		return true
	}
	// Files inside a references/ subdirectory are sub-files
	if strings.Contains(lower, "/references/") {
		return true
	}
	// If there's a SKILL.md in the same directory, this file is a sub-file
	// (it's a sibling of the main skill file)
	dir := filepath.Dir(filePath)
	skillPath := filepath.Join(dir, "SKILL.md")
	for _, n := range lookup {
		if strings.EqualFold(n.FilePath, skillPath) {
			return true
		}
	}
	return false
}

// rankNodes scores all skill nodes for a query and returns sorted results.
// If withGraphBoost is true, connected high-scoring nodes boost each other
// (capped at 30% of the node's own score), matching `memgraph recommend` and
// the /api/search V2 handler behavior. limit <= 0 means no limit.
func rankNodes(graph *GraphIndex, lookup map[string]Memory, query string, limit int, withGraphBoost bool) []QueryResultItem {
	relatedMap := buildRelatedMap(graph)
	idf := computeIDF(graph, splitQueryWords(query))

	type scored struct {
		node  Memory
		score float64
	}
	var results []scored
	for _, n := range graph.Nodes {
		if n.Type == "namespace" {
			continue
		}
		// Filter out sub-file references (README.md, references/*.md, etc.)
		if n.FilePath != "" && isSubFileReference(n.FilePath, lookup) {
			continue
		}
		s := scoreNode(n, query, idf)
		if s > 0 {
			results = append(results, scored{node: n, score: s})
		}
	}

	if withGraphBoost {
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
					results[i].score += otherScore * 0.02
				}
			}
			// Cap graph boost at 30% of the node's own score to prevent
			// highly-connected nodes from dominating over better word matches
			if results[i].score > r.score*1.3 {
				results[i].score = r.score * 1.3
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

	items := make([]QueryResultItem, 0, len(results))
	for _, r := range results {
		items = append(items, QueryResultItem{
			ID:          r.node.ID,
			Name:        r.node.Name,
			Description: r.node.Description,
			Project:     r.node.Project,
			FilePath:    r.node.FilePath,
			Score:       r.score,
			Tags:        r.node.Tags,
			Related:     getRelatedForNodeLimit(r.node.ID, relatedMap, lookup, 5),
		})
	}
	return items
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
		if jsonOutput {
			errorResponse(85, "invalid_argument", "Query text required for query command", false)
		} else {
			fmt.Fprintln(os.Stderr, "Usage: memgraph query <text> [--limit N] [--json]")
		}
		os.Exit(85)
	}

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		if jsonOutput {
			errorResponse(92, "graph_load_error", fmt.Sprintf("%v", err), false)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(92)
	}

	items := rankNodes(graph, lookup, query, limit, false)

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
	limit := 10

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
			if target == "" && !strings.HasPrefix(args[i], "--memory-dir=") && !strings.HasPrefix(args[i], "--") {
				target = args[i]
			}
		}
	}

	if target == "" {
		if jsonOutput {
			errorResponse(85, "invalid_argument", "Skill ID or name required for related command", false)
		} else {
			fmt.Fprintln(os.Stderr, "Usage: memgraph related <skill-id-or-name> [--json]")
		}
		os.Exit(85)
	}

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		if jsonOutput {
			errorResponse(92, "graph_load_error", fmt.Sprintf("%v", err), false)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
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
		if jsonOutput {
			errorResponse(92, "skill_not_found", fmt.Sprintf("Skill not found: %s", target), false)
		} else {
			fmt.Fprintf(os.Stderr, "Skill not found: %s\n", target)
		}
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

	// Apply limit (default 10) to keep output manageable for agents
	totalRelated := len(related)
	if limit > 0 && len(related) > limit {
		related = related[:limit]
	}

	result := RelatedResult{
		Skill: QueryResultItem{
			ID:          node.ID,
			Name:        node.Name,
			Description: node.Description,
			Project:     node.Project,
			FilePath:    node.FilePath,
			Tags:        node.Tags,
		},
		Count:   totalRelated,
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
		if jsonOutput {
			errorResponse(85, "invalid_argument", "Task description required for recommend command", false)
		} else {
			fmt.Fprintln(os.Stderr, "Usage: memgraph recommend <task description> [--limit N] [--json]")
		}
		os.Exit(85)
	}

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		if jsonOutput {
			errorResponse(92, "graph_load_error", fmt.Sprintf("%v", err), false)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(92)
	}

	items := rankNodes(graph, lookup, task, limit, true)

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
