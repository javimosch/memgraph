package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type skillInput struct {
	ID          string
	DirName     string
	Name        string
	Description string
	Project     string
	Content     string
	Tags        []string
	Created     time.Time
	FilePath    string
}

type skillGraph struct {
	Nodes []Memory
	Edges []GraphEdge
}

func extractSignificantTerms(text string) map[string]bool {
	terms := make(map[string]bool)
	for _, t := range tokenize(text) {
		if len(t.Term) >= 3 && !stopWords[t.Term] {
			terms[t.Term] = true
		}
	}
	return terms
}

func intersectTerms(a, b map[string]bool) []string {
	shared := make([]string, 0)
	for term := range a {
		if b[term] {
			shared = append(shared, term)
		}
	}
	sort.Strings(shared)
	return shared
}

func newestTime(skills []skillInput) time.Time {
	var t time.Time
	for _, s := range skills {
		if s.Created.After(t) {
			t = s.Created
		}
	}
	return t
}

func addLinkUnique(links map[string][]Link, source, target, relation, value string) {
	for _, l := range links[source] {
		if l.Target == target && l.Relation == relation {
			return
		}
	}
	links[source] = append(links[source], Link{Target: target, Relation: relation, Value: value})
}

func buildTFIDFVectors(skills []skillInput) map[string]map[string]float64 {
	tf := make(map[string]map[string]int)
	df := make(map[string]int)
	n := len(skills)

	for _, s := range skills {
		seen := make(map[string]bool)
		for _, t := range tokenize(s.Description) {
			if tf[t.Term] == nil {
				tf[t.Term] = make(map[string]int)
			}
			tf[t.Term][s.ID]++
			if !seen[t.Term] {
				df[t.Term]++
				seen[t.Term] = true
			}
		}
	}

	vectors := make(map[string]map[string]float64)
	for _, s := range skills {
		vectors[s.ID] = make(map[string]float64)
	}

	for term, idFreq := range tf {
		idf := math.Log(float64(n+1) / float64(df[term]+1))
		for id, freq := range idFreq {
			vectors[id][term] = float64(freq) * idf
		}
	}
	return vectors
}

func vectorNorm(v map[string]float64) float64 {
	sum := 0.0
	for _, val := range v {
		sum += val * val
	}
	return math.Sqrt(sum)
}

func cosineSimilarity(vA, vB map[string]float64, normA, normB float64) float64 {
	if normA == 0 || normB == 0 {
		return 0
	}
	dot := 0.0
	for term, a := range vA {
		if b, ok := vB[term]; ok {
			dot += a * b
		}
	}
	return dot / (normA * normB)
}

func buildSkillGraph(skills []skillInput) *skillGraph {
	graph := &skillGraph{}
	links := make(map[string][]Link)
	nodesByID := make(map[string]Memory)

	byProject := make(map[string][]skillInput)
	for _, s := range skills {
		nodesByID[s.ID] = Memory{
			ID:          s.ID,
			Name:        s.Name,
			Description: s.Description,
			Type:        "skill",
			Project:     s.Project,
			Session:     "graph-from-dir",
			Tags:        s.Tags,
			Created:     s.Created,
			Content:     s.Content,
			FilePath:    s.FilePath,
		}
		byProject[s.Project] = append(byProject[s.Project], s)
	}

	var projects []string
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	for _, project := range projects {
		members := byProject[project]
		sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
		hubID := "namespace_" + project
		var contentLines []string
		for _, m := range members {
			contentLines = append(contentLines, "- "+m.Name+" ("+m.ID+")")
		}
		hub := Memory{
			ID:          hubID,
			Name:        project + " namespace",
			Description: "Namespace hub for " + project,
			Type:        "namespace",
			Project:     project,
			Session:     "graph-from-dir",
			Tags:        []string{project, "namespace"},
			Created:     newestTime(members),
			Content:     strings.Join(contentLines, "\n"),
		}
		nodesByID[hubID] = hub
		graph.Nodes = append(graph.Nodes, hub)
		for _, m := range members {
			addLinkUnique(links, m.ID, hubID, "namespace", "")
		}
	}

	for _, a := range skills {
		text := strings.ToLower(a.Content + " " + a.Description)
		for _, b := range skills {
			if a.ID == b.ID {
				continue
			}
			queries := []string{strings.ToLower(b.DirName), strings.ToLower(b.Name)}
			for _, q := range queries {
				if q != "" && strings.Contains(text, q) {
					addLinkUnique(links, a.ID, b.ID, "references", "")
					break
				}
			}
		}
	}

	termSets := make(map[string]map[string]bool)
	for _, s := range skills {
		termSets[s.ID] = extractSignificantTerms(s.Description)
	}
	for i := 0; i < len(skills); i++ {
		for j := i + 1; j < len(skills); j++ {
			a, b := skills[i], skills[j]
			shared := intersectTerms(termSets[a.ID], termSets[b.ID])
			if len(shared) >= 2 {
				value := strings.Join(shared, " ")
				addLinkUnique(links, a.ID, b.ID, "shared-keyword", value)
				addLinkUnique(links, b.ID, a.ID, "shared-keyword", value)
			}
		}
	}

	vectors := buildTFIDFVectors(skills)
	norms := make(map[string]float64)
	for id, v := range vectors {
		norms[id] = vectorNorm(v)
	}

	similarity := make(map[string]map[string]float64)
	for i := 0; i < len(skills); i++ {
		a := skills[i]
		similarity[a.ID] = make(map[string]float64)
		for j := 0; j < len(skills); j++ {
			if i == j {
				continue
			}
			b := skills[j]
			sim := cosineSimilarity(vectors[a.ID], vectors[b.ID], norms[a.ID], norms[b.ID])
			if sim > 0.05 {
				similarity[a.ID][b.ID] = sim
			}
		}
	}

	for _, a := range skills {
		type pair struct {
			id  string
			sim float64
		}
		var candidates []pair
		for id, sim := range similarity[a.ID] {
			candidates = append(candidates, pair{id: id, sim: sim})
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].sim != candidates[j].sim {
				return candidates[i].sim > candidates[j].sim
			}
			return candidates[i].id < candidates[j].id
		})
		if len(candidates) > 3 {
			candidates = candidates[:3]
		}
		for _, c := range candidates {
			value := strconv.FormatFloat(c.sim, 'f', 4, 64)
			addLinkUnique(links, a.ID, c.id, "similar", value)
		}
	}

	for _, s := range skills {
		m := nodesByID[s.ID]
		m.Links = links[s.ID]
		graph.Nodes = append(graph.Nodes, m)
		for _, l := range m.Links {
			graph.Edges = append(graph.Edges, GraphEdge{Source: s.ID, Target: l.Target, Relation: l.Relation, Value: l.Value})
		}
	}

	return graph
}

func saveGraphIndex(memoryPath string, graph *skillGraph) error {
	index := GraphIndex{
		Version: 1,
		Nodes:   graph.Nodes,
		Edges:   graph.Edges,
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(memoryPath, "graph.json"), data, 0644)
}

func loadGraphIndex(memoryPath string) (*GraphIndex, error) {
	data, err := os.ReadFile(filepath.Join(memoryPath, "graph.json"))
	if err != nil {
		return nil, err
	}
	var index GraphIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return &index, nil
}

func loadGraphForServe(memoryPath string, index *SearchIndex) (*GraphIndex, map[string]Memory, error) {
	graph, err := loadGraphIndex(memoryPath)
	if err == nil {
		lookup := make(map[string]Memory, len(graph.Nodes))
		for _, n := range graph.Nodes {
			lookup[n.ID] = n
		}
		return graph, lookup, nil
	}
	if index == nil {
		return nil, nil, fmt.Errorf("graph.json not found and no search index available; run 'memgraph graph-from-dir <dir>'")
	}
	graph = &GraphIndex{
		Version: 1,
		Nodes:   make([]Memory, 0, len(index.Memories)),
		Edges:   make([]GraphEdge, 0),
	}
	for _, m := range index.Memories {
		graph.Nodes = append(graph.Nodes, m)
	}
	sort.Slice(graph.Nodes, func(i, j int) bool { return graph.Nodes[i].ID < graph.Nodes[j].ID })
	for _, n := range graph.Nodes {
		for _, l := range n.Links {
			graph.Edges = append(graph.Edges, GraphEdge{
				Source:   n.ID,
				Target:   l.Target,
				Relation: l.Relation,
				Value:    l.Value,
			})
		}
	}
	lookup := make(map[string]Memory, len(graph.Nodes))
	for _, n := range graph.Nodes {
		lookup[n.ID] = n
	}
	return graph, lookup, nil
}
