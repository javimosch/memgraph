package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Plan ingestion and listing commands.
// Split from cmd_plans.go to keep files under 500 LOC.

// handlePlansList lists all indexed plans in the graph.
func handlePlansList(cfg *Config) {
	graph, err := loadGraphIndex(cfg.MemoryDir)
	if err != nil {
		errorResponse(92, "resource_error", "Failed to load graph: "+err.Error(), false)
		os.Exit(92)
	}

	var plans []Memory
	for _, node := range graph.Nodes {
		if node.Type == "plan" {
			plans = append(plans, node)
		}
	}

	if jsonOutput {
		fmt.Printf("{\"plans\":%d,\"items\":[", len(plans))
		for i, p := range plans {
			if i > 0 {
				fmt.Print(",")
			}
			fmt.Printf(`{"id":"%s","name":"%s","project":"%s","description":"%s"}`,
				p.ID, p.Name, p.Project, truncate(p.Description, 80))
		}
		fmt.Println("]}")
	} else {
		if len(plans) == 0 {
			fmt.Println("No plans indexed. Run: memgraph graph-from-dir --include-plans")
			return
		}
		fmt.Printf("Indexed plans (%d):\n", len(plans))
		for _, p := range plans {
			fmt.Printf("  %s  %s  [%s]\n", p.ID, p.Name, p.Project)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// ingestPlans discovers plan files in common project directories, converts
// them to graph nodes (type "plan"), and appends them to the existing graph.
// Returns the number of plans indexed.
// If extraDirs is provided, those directories are also scanned.
func ingestPlans(targetDir string, cfg *Config, extraDirs ...string) int {
	planDirs := discoverPlanDirs()
	planDirs = append(planDirs, extraDirs...)
	if len(planDirs) == 0 {
		return 0
	}

	var allPlans []planInput
	seenIDs := make(map[string]bool)
	for _, dir := range planDirs {
		plans, err := scanPlanFiles(dir)
		if err != nil {
			continue
		}
		for _, p := range plans {
			if seenIDs[p.ID] {
				continue
			}
			seenIDs[p.ID] = true
			allPlans = append(allPlans, p)
		}
	}

	if len(allPlans) == 0 {
		return 0
	}

	// Load existing graph, append plan nodes, save
	graph, err := loadGraphIndex(targetDir)
	if err != nil {
		return 0
	}

	for _, pi := range allPlans {
		node := Memory{
			ID:          pi.ID,
			Name:        pi.Name,
			Description: pi.Description,
			Type:        "plan",
			Project:     pi.Project,
			Tags:        pi.Tags,
			Created:     pi.Created,
			Content:     pi.Content,
			FilePath:    pi.FilePath,
		}
		graph.Nodes = append(graph.Nodes, node)
	}

	// Save the updated graph
	if err := saveGraphIndex(targetDir, &skillGraph{
		Nodes: graph.Nodes,
		Edges: graph.Edges,
	}); err != nil {
		return 0
	}

	if cfg != nil {
		updateSearchIndex(cfg)
	}

	return len(allPlans)
}

// discoverPlanDirs finds directories that might contain plan files.
// Scans common project locations.
func discoverPlanDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "ai"),
		filepath.Join(home, "projets"),
		filepath.Join(home, "projects"),
		filepath.Join(home, "repos"),
		filepath.Join(home, "pr"),
	}
	var dirs []string
	for _, d := range candidates {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
