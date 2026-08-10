package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// mcpRecommend implements the memgraph_recommend tool.
func mcpRecommend(cfg *Config, args map[string]any) string {
	task := mcpGetString(args, "task")
	if task == "" {
		return "Error: task is required"
	}
	limit := mcpGetInt(args, "limit", 5)

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		return "No skill graph found. Run memgraph_graph_from_dir to build one."
	}

	relatedMap := buildRelatedMap(graph)
	ranked := rankNodes(graph, lookup, task, limit, true, false)

	if len(ranked) == 0 {
		return fmt.Sprintf("No skills found matching: %s", task)
	}

	var results []QueryResultItem
	for _, node := range ranked {
		item := QueryResultItem{
			ID:          node.ID,
			Name:        node.Name,
			Description: node.Description,
			Project:     node.Project,
			FilePath:    node.FilePath,
			Score:       node.Score,
			Type:        node.Type,
			Tags:        node.Tags,
		}
		if related, ok := relatedMap[node.ID]; ok {
			for _, r := range related {
				if rNode, ok2 := lookup[r.Target]; ok2 {
					item.Related = append(item.Related, RelatedNode{
						ID:       rNode.ID,
						Name:     rNode.Name,
						Relation: r.Relation,
						Project:  rNode.Project,
						FilePath: rNode.FilePath,
					})
				}
			}
		}
		results = append(results, item)
	}

	out, _ := json.Marshal(map[string]any{
		"task":        task,
		"count":       len(results),
		"recommended": results,
	})
	return string(out)
}

// mcpQuery implements the memgraph_query tool.
func mcpQuery(cfg *Config, args map[string]any) string {
	keywords := mcpGetString(args, "keywords")
	if keywords == "" {
		return "Error: keywords is required"
	}
	limit := mcpGetInt(args, "limit", 10)

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		return "No skill graph found. Run memgraph_graph_from_dir to build one."
	}

	relatedMap := buildRelatedMap(graph)
	ranked := rankNodes(graph, lookup, keywords, limit, true, false)

	if len(ranked) == 0 {
		return fmt.Sprintf("No skills found matching: %s", keywords)
	}

	var results []QueryResultItem
	for _, node := range ranked {
		item := QueryResultItem{
			ID:          node.ID,
			Name:        node.Name,
			Description: node.Description,
			Project:     node.Project,
			FilePath:    node.FilePath,
			Score:       node.Score,
			Type:        node.Type,
			Tags:        node.Tags,
		}
		if related, ok := relatedMap[node.ID]; ok {
			for _, r := range related {
				if rNode, ok2 := lookup[r.Target]; ok2 {
					item.Related = append(item.Related, RelatedNode{
						ID:       rNode.ID,
						Name:     rNode.Name,
						Relation: r.Relation,
						Project:  rNode.Project,
						FilePath: rNode.FilePath,
					})
				}
			}
		}
		results = append(results, item)
	}

	out, _ := json.Marshal(map[string]any{
		"query":   keywords,
		"count":   len(results),
		"results": results,
	})
	return string(out)
}

// mcpRelated implements the memgraph_related tool.
func mcpRelated(cfg *Config, args map[string]any) string {
	target := mcpGetString(args, "target")
	if target == "" {
		return "Error: target is required"
	}
	limit := mcpGetInt(args, "limit", 10)

	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		return "No skill graph found. Run memgraph_graph_from_dir to build one."
	}

	// Find the target skill by ID or name
	var targetMemory Memory
	found := false
	for id, mem := range lookup {
		if id == target || mem.Name == target {
			targetMemory = mem
			found = true
			break
		}
	}
	if !found {
		return fmt.Sprintf("Skill %q not found in graph", target)
	}

	relatedMap := buildRelatedMap(graph)
	related := relatedMap[targetMemory.ID]

	var relatedNodes []RelatedNode
	count := 0
	for _, r := range related {
		if limit > 0 && count >= limit {
			break
		}
		if rNode, ok := lookup[r.Target]; ok {
			relatedNodes = append(relatedNodes, RelatedNode{
				ID:       rNode.ID,
				Name:     rNode.Name,
				Relation: r.Relation,
				Project:  rNode.Project,
				FilePath: rNode.FilePath,
			})
			count++
		}
	}

	out, _ := json.Marshal(map[string]any{
		"skill": QueryResultItem{
			ID:          targetMemory.ID,
			Name:        targetMemory.Name,
			Description: targetMemory.Description,
			Project:     targetMemory.Project,
			FilePath:    targetMemory.FilePath,
			Type:        targetMemory.Type,
			Tags:        targetMemory.Tags,
		},
		"count":   len(relatedNodes),
		"related": relatedNodes,
	})
	return string(out)
}

// mcpPlans implements the memgraph_plans tool.
func mcpPlans(cfg *Config) string {
	graph, err := loadGraphIndex(cfg.MemoryDir)
	if err != nil {
		return "No skill graph found. Run memgraph_graph_from_dir to build one."
	}

	var plans []map[string]any
	for _, node := range graph.Nodes {
		if node.Type == "plan" {
			plans = append(plans, map[string]any{
				"id":          node.ID,
				"name":        node.Name,
				"description": node.Description,
				"project":     node.Project,
				"file_path":   node.FilePath,
			})
		}
	}

	out, _ := json.Marshal(map[string]any{
		"count": len(plans),
		"plans": plans,
	})
	return string(out)
}

// mcpGraphFromDir implements the memgraph_graph_from_dir tool.
func mcpGraphFromDir(cfg *Config, args map[string]any) string {
	syncDirsRaw := args["sync_dirs"]
	if syncDirsRaw == nil {
		return "Error: sync_dirs is required"
	}

	var syncDirs []string
	if arr, ok := syncDirsRaw.([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				syncDirs = append(syncDirs, s)
			}
		}
	}
	if len(syncDirs) == 0 {
		return "Error: sync_dirs must be a non-empty array of strings"
	}

	includePlans := mcpGetBool(args, "include_plans", false)

	// Use the existing ingest infrastructure
	// ingestMultiDir(sourceDirs []string, targetDir string, cfg *Config)
	targetDir := filepath.Join(getGlobalMemgraphDir(), "skills-graph")
	graph, skillCount, namespaceCount, err := ingestMultiDir(syncDirs, targetDir, cfg)
	if err != nil {
		return fmt.Sprintf("Failed to build graph: %v", err)
	}

	out, _ := json.Marshal(map[string]any{
		"status":           "built",
		"skills_indexed":   skillCount,
		"namespaces":       namespaceCount,
		"edges":            len(graph.Edges),
		"sync_dirs":        syncDirs,
		"include_plans":    includePlans,
	})
	return string(out)
}
