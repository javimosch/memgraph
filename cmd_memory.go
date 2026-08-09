package main

import (
	"encoding/json"
	"fmt"
	"io"
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
		if jsonOutput || noInteractive {
			errorResponse(85, "invalid_argument", "Content required for remember command", false)
			os.Exit(85)
		}
		fmt.Fprintf(os.Stderr, "Enter memory content (Ctrl-D to finish):\n")
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			errorResponse(110, "io_error", fmt.Sprintf("Failed to read stdin: %v", err), false)
			os.Exit(110)
		}
		content = strings.TrimSpace(string(data))
		if content == "" {
			errorResponse(85, "invalid_argument", "Content required for remember command (stdin was empty)", false)
			os.Exit(85)
		}
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
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
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

	format := opts.Format
	if !opts.FormatSet {
		format = "full"
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
		if format == "index" || format == "paths" {
			printSectionIndex(results, cfg.MemoryDir, format)
		} else {
			if jsonOutput {
				json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
					"count":   len(results),
					"results": results,
				})
			} else {
				fmt.Printf("All memories in %s:\n\n", cfg.MemoryDir)
				for _, result := range results {
					printResult(result)
				}
				fmt.Printf("Total memories: %d\n", len(results))
			}
		}
		return
	}

	results := searchMemories(index, query, searchOpts)
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	if len(results) == 0 {
		if jsonOutput {
			json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
				"query":   query,
				"count":   0,
				"results": []SearchResult{},
			})
		} else {
			fmt.Printf("No memories found matching: %s\n", query)
		}
		return
	}

	if format == "index" || format == "paths" {
		printSectionIndex(results, cfg.MemoryDir, format)
		return
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"query":   query,
			"count":   len(results),
			"results": results,
		})
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
			Sections:   memory.Sections,
			FilePath:   memory.FilePath,
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

// printSectionIndex outputs a compact section index instead of full content.
// format "index" shows slug + line range + preview.
// format "paths" shows the file path + absolute line ranges for agent read tools.
func printSectionIndex(results []SearchResult, memoryDir, format string) {
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"count":   len(results),
			"format":  format,
			"results": results,
		})
		return
	}

	for _, result := range results {
		var path string
		if result.FilePath != "" {
			path = result.FilePath
		} else {
			path = filepath.Join(memoryDir, "memory_"+result.MemoryID+".md")
		}

		// Compute frontmatter offset for absolute line numbers in paths mode.
		absOffset := 0
		if format == "paths" {
			if data, err := os.ReadFile(path); err == nil {
				lines := strings.Split(string(data), "\n")
				dashCount := 0
				for i, line := range lines {
					if strings.TrimSpace(line) == "---" {
						dashCount++
						if dashCount == 2 {
							absOffset = i + 1
							break
						}
					}
				}
			}
		}

		if format == "paths" {
			fmt.Println(path)
		} else {
			fmt.Printf("Memory %s — %q (score: %.2f)\n", result.MemoryID, result.Title, result.Score)
		}

		for _, sec := range result.Sections {
			if format == "paths" {
				absStart := absOffset + sec.LineStart
				absEnd := absOffset + sec.LineEnd
				lineCount := absEnd - absStart + 1
				fmt.Printf("  L%d-%d (%d lines):  [%s]  %q\n", absStart, absEnd, lineCount, sec.Slug, sec.Title)
			} else {
				fmt.Printf("  [%s]  L%d-%d  %q\n", sec.Slug, sec.LineStart, sec.LineEnd, sec.Preview)
			}
		}
		fmt.Println()
	}

	if format != "paths" {
		fmt.Println("→ read full:    memgraph read <id>")
		fmt.Println("→ read section: memgraph read <id>/<slug>")
	}
}

// handleRead implements `memgraph read <id>` and `memgraph read <id>/<slug>`.
// It reads the memory file from disk and returns either the full content
// or a single section's content.
func handleRead(cfg *Config) {
	positional, _ := parseCommandArgs(os.Args[2:])
	if len(positional) == 0 {
		if jsonOutput {
			errorResponse(85, "invalid_argument", "Usage: memgraph read <id> or memgraph read <id>/<slug>", false)
		} else {
			fmt.Fprintf(os.Stderr, "Usage: memgraph read <id>\n       memgraph read <id>/<slug>\n")
		}
		os.Exit(85)
	}

	target := positional[0]
	memoryID := target
	slugFilter := ""

	if idx := strings.Index(target, "/"); idx >= 0 {
		memoryID = target[:idx]
		slugFilter = target[idx+1:]
	}

	filePath, found := findMemoryFileByID(cfg.MemoryDir, memoryID)
	if !found {
		if jsonOutput {
			errorResponse(92, "resource_not_found", fmt.Sprintf("Memory %s not found", memoryID), false)
		} else {
			fmt.Fprintf(os.Stderr, "Memory %s not found in %s\n", memoryID, cfg.MemoryDir)
		}
		os.Exit(92)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to read memory file: %v", err), false)
		os.Exit(92)
	}

	memory := parseMemory(string(data), filepath.Base(filePath))

	if slugFilter == "" {
		// Full memory
		if jsonOutput {
			json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
				"id":       memory.ID,
				"name":     memory.Name,
				"type":     memory.Type,
				"project":  memory.Project,
				"tags":     memory.Tags,
				"created":  memory.Created.Format(time.RFC3339),
				"sections": memory.Sections,
				"content":  memory.Content,
				"file_path": filePath,
			})
		} else {
			fmt.Printf("Memory %s — %s\n", memory.ID, memory.Name)
			fmt.Printf("Type: %s  Project: %s  Created: %s\n", memory.Type, memory.Project, memory.Created.Format(time.RFC3339))
			if len(memory.Tags) > 0 {
				fmt.Printf("Tags: %s\n", strings.Join(memory.Tags, ", "))
			}
			fmt.Printf("File: %s\n\n", filePath)
			for _, sec := range memory.Sections {
				fmt.Printf("[%s] (L%d-%d)\n", sec.Slug, sec.LineStart, sec.LineEnd)
			}
			fmt.Println()
			fmt.Println(memory.Content)
		}
		return
	}

	// Section read — find the matching section
	var matched *Section
	for i := range memory.Sections {
		if memory.Sections[i].Slug == slugFilter {
			matched = &memory.Sections[i]
			break
		}
	}
	if matched == nil {
		if jsonOutput {
			errorResponse(85, "invalid_argument", fmt.Sprintf("Section [%s] not found in memory %s", slugFilter, memoryID), false)
		} else {
			fmt.Fprintf(os.Stderr, "Section [%s] not found in memory %s\n", slugFilter, memoryID)
			fmt.Fprintf(os.Stderr, "Available sections: ")
			for i, sec := range memory.Sections {
				if i > 0 {
					fmt.Fprintf(os.Stderr, ", ")
				}
				fmt.Fprintf(os.Stderr, "[%s]", sec.Slug)
			}
			fmt.Fprintf(os.Stderr, "\n")
		}
		os.Exit(85)
	}

	// Extract section content from the memory file lines.
	// We need to read the raw file to get accurate line numbers including frontmatter.
	rawLines := strings.Split(string(data), "\n")
	// Find where content starts (after the second "---" of frontmatter).
	contentOffset := 0
	dashCount := 0
	for i, line := range rawLines {
		if strings.TrimSpace(line) == "---" {
			dashCount++
			if dashCount == 2 {
				contentOffset = i + 1
				break
			}
		}
	}

	// Section line numbers are relative to the content (after frontmatter).
	// Convert to absolute file line numbers.
	absStart := contentOffset + matched.LineStart - 1
	absEnd := contentOffset + matched.LineEnd
	if absStart < 0 {
		absStart = 0
	}
	if absEnd > len(rawLines) {
		absEnd = len(rawLines)
	}

	sectionContent := strings.Join(rawLines[absStart:absEnd], "\n")

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"id":         memory.ID,
			"slug":       matched.Slug,
			"title":      matched.Title,
			"line_start": matched.LineStart,
			"line_end":   matched.LineEnd,
			"file_path":  filePath,
			"content":    strings.TrimSpace(sectionContent),
		})
	} else {
		fmt.Printf("[%s] from memory %s\n", matched.Slug, memory.ID)
		fmt.Printf("File: %s (content L%d-%d)\n\n", filePath, matched.LineStart, matched.LineEnd)
		fmt.Println(strings.TrimSpace(sectionContent))
	}
}
