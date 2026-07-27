package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func handleRemember(cfg *Config) {
	positional, opts := parseCommandArgs(os.Args[2:])

	content := ""
	if opts.TextSet {
		content = opts.Text
	} else if len(positional) > 0 {
		content = strings.Join(positional, " ")
	}

	if content == "" {
		if noInteractive {
			errorResponse(85, "invalid_argument", "Content required for remember command", false)
			os.Exit(85)
		}
		fmt.Fprintf(os.Stderr, "Enter memory content (Ctrl-D to finish):\n")
		errorResponse(110, "not_implemented", "Interactive input not yet implemented", false)
		os.Exit(110)
	}

	session := opts.Session
	if session == "" {
		session = os.Getenv("SICK_MEMORY_SESSION")
	}
	if session == "" {
		session = "default"
	}

	writeMemoryFromInput(cfg, content, opts.MemoryType, opts.Project, session, opts.Tags)
}

func writeMemoryFromInput(cfg *Config, content, memoryType, project, session string, tags []string) string {
	if memoryType == "" {
		memoryType = cfg.GlobalConfig.DefaultMemoryType
	}

	timestamp := time.Now().UTC()
	memoryID := fmt.Sprintf("%d", timestamp.UnixNano())
	filename := fmt.Sprintf("memory_%s.md", memoryID)
	filePath := filepath.Join(cfg.MemoryDir, filename)

	memory := Memory{
		ID:          memoryID,
		Name:        "Memory " + memoryID,
		Description: content,
		Type:        memoryType,
		Project:     project,
		Session:     session,
		Tags:        tags,
		Created:     timestamp,
		Content:     content,
	}

	_ = os.MkdirAll(cfg.MemoryDir, 0755)
	if err := os.WriteFile(filePath, []byte(formatMemoryFile(memory)), 0644); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to write memory file: %v", err), false)
		os.Exit(92)
	}

	updateSearchIndex(cfg)

	if jsonOutput {
		successResponse(map[string]interface{}{
			"id":      memoryID,
			"status":  "remembered",
			"type":    memoryType,
			"project": project,
			"session": session,
			"tags":    tags,
		})
	} else {
		fmt.Printf("Memory saved with ID: %s\n", memoryID)
	}
	return memoryID
}

func handleRecall(cfg *Config) {
	positional, opts := parseCommandArgs(os.Args[2:])

	query := ""
	if opts.QuerySet {
		query = opts.Query
	} else if len(positional) > 0 {
		query = strings.Join(positional, " ")
	}

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		errorResponse(92, "resource_not_found", "Memory directory not found", false)
		os.Exit(92)
	}

	searchOpts := SearchOptions{
		Project: opts.Project,
		Session: opts.Session,
		Tags:    opts.Tags,
		TagOnly: opts.TagOnly,
		Weights: cfg.GlobalConfig.SearchWeights,
	}
	if opts.WeightsJSON != "" {
		if w, ok := parseSearchWeightsJSON(opts.WeightsJSON); ok {
			searchOpts.Weights = mergeSearchWeights(searchOpts.Weights, w)
		}
	}

	if query == "" {
		results := listAllMemories(index, searchOpts.Project, searchOpts.Session, searchOpts.Tags, opts.Limit)
		if jsonOutput {
			successResponse(results)
		} else {
			fmt.Printf("All memories in %s:\n\n", cfg.MemoryDir)
			for _, result := range results {
				printResult(result)
			}
			fmt.Printf("Total memories: %d\n", len(results))
		}
		return
	}

	results := searchMemories(index, query, searchOpts)
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	if len(results) == 0 {
		if jsonOutput {
			successResponse([]SearchResult{})
		} else {
			fmt.Printf("No memories found matching: %s\n", query)
		}
		return
	}

	if jsonOutput {
		successResponse(results)
	} else {
		fmt.Printf("Found %d memories matching: %s\n\n", len(results), query)
		for _, result := range results {
			printResult(result)
		}
	}
}

func listAllMemories(index *SearchIndex, project, session string, tags []string, limit int) []SearchResult {
	var results []SearchResult
	for _, memory := range index.Memories {
		if project != "" && memory.Project != project {
			continue
		}
		if session != "" && memory.Session != session {
			continue
		}
		if !memoryHasAllTags(memory.Tags, tags) {
			continue
		}
		hoursSinceCreation := time.Since(memory.Created).Hours()
		score := 100.0 - hoursSinceCreation
		results = append(results, SearchResult{
			MemoryID:   memory.ID,
			Score:      score,
			Title:      memory.Name,
			Content:    memory.Content,
			MemoryType: memory.Type,
			Project:    memory.Project,
			Session:    memory.Session,
			Tags:       memory.Tags,
			Created:    memory.Created.Format(time.RFC3339),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

func printResult(result SearchResult) {
	fmt.Printf("ID: %s (score: %.2f)\n", result.MemoryID, result.Score)
	fmt.Printf("Type: %s\n", result.MemoryType)
	if result.Project != "" {
		fmt.Printf("Project: %s\n", result.Project)
	}
	if len(result.Tags) > 0 {
		fmt.Printf("Tags: %s\n", strings.Join(result.Tags, ", "))
	}
	fmt.Printf("Created: %s\n", result.Created)
	fmt.Printf("%s\n\n", result.Content)
}
