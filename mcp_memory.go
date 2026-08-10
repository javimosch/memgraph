package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// mcpRecall implements the memgraph_recall tool.
func mcpRecall(cfg *Config, args map[string]any) string {
	query := mcpGetString(args, "query")
	if query == "" {
		return "Error: query is required"
	}

	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	format := mcpGetString(args, "format")
	if format == "" {
		format = "index"
	}
	limit := mcpGetInt(args, "limit", 10)
	tags := mcpGetStringSlice(args, "tags")

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		return "No memories found (directory not initialized)."
	}

	searchOpts := SearchOptions{
		Project: "",
		Tags:    tags,
		Weights: cfg.GlobalConfig.SearchWeights,
	}
	if !cfg.ScopeResolved {
		searchOpts.Project = projectName
	}

	results := searchMemories(index, query, searchOpts)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	if len(results) == 0 {
		return fmt.Sprintf("No memories found matching: %s", query)
	}

	if format == "full" {
		data, _ := json.Marshal(map[string]any{
			"query":   query,
			"count":   len(results),
			"results": results,
		})
		return string(data)
	}

	var lines []string
	for _, result := range results {
		lines = append(lines, fmt.Sprintf("Memory %s — %q (score: %.2f)", result.MemoryID, result.Title, result.Score))
		for _, sec := range result.Sections {
			lines = append(lines, fmt.Sprintf("  [%s]  L%d-%d  %q", sec.Slug, sec.LineStart, sec.LineEnd, sec.Preview))
		}
		lines = append(lines, "")
	}
	lines = append(lines, "Use memgraph_read with id and slug to read a specific section.")
	return strings.Join(lines, "\n")
}

// mcpRead implements the memgraph_read tool.
func mcpRead(cfg *Config, args map[string]any) string {
	id := mcpGetString(args, "id")
	if id == "" {
		return "Error: id is required"
	}
	slug := mcpGetString(args, "slug")
	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	filePath, found := findMemoryFileByID(cfg.MemoryDir, id)
	if !found {
		return fmt.Sprintf("Memory %s not found", id)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Sprintf("Failed to read memory file: %v", err)
	}

	memory := parseMemory(string(data), filepath.Base(filePath))

	if slug == "" {
		out, _ := json.Marshal(map[string]any{
			"id":        memory.ID,
			"name":      memory.Name,
			"type":      memory.Type,
			"project":   memory.Project,
			"tags":      memory.Tags,
			"created":   memory.Created.Format(time.RFC3339),
			"sections":  memory.Sections,
			"content":   memory.Content,
			"file_path": filePath,
		})
		return string(out)
	}

	var matched *Section
	for i := range memory.Sections {
		if memory.Sections[i].Slug == slug {
			matched = &memory.Sections[i]
			break
		}
	}
	if matched == nil {
		var available []string
		for _, sec := range memory.Sections {
			available = append(available, sec.Slug)
		}
		return fmt.Sprintf("Section [%s] not found. Available sections: %s", slug, strings.Join(available, ", "))
	}

	rawLines := strings.Split(string(data), "\n")
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
	absStart := contentOffset + matched.LineStart - 1
	absEnd := contentOffset + matched.LineEnd
	if absStart < 0 {
		absStart = 0
	}
	if absEnd > len(rawLines) {
		absEnd = len(rawLines)
	}
	sectionContent := strings.TrimSpace(strings.Join(rawLines[absStart:absEnd], "\n"))

	out, _ := json.Marshal(map[string]any{
		"id":         memory.ID,
		"slug":       matched.Slug,
		"title":      matched.Title,
		"line_start": matched.LineStart,
		"line_end":   matched.LineEnd,
		"file_path":  filePath,
		"content":    sectionContent,
	})
	return string(out)
}

// mcpSave implements the memgraph_save tool.
func mcpSave(cfg *Config, args map[string]any) string {
	text := mcpGetString(args, "text")
	if text == "" {
		return "Error: text is required"
	}

	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	memoryType := mcpGetString(args, "type")
	if memoryType == "" {
		memoryType = cfg.GlobalConfig.DefaultMemoryType
		if memoryType == "" {
			memoryType = "user"
		}
	}
	tags := mcpGetStringSlice(args, "tags")

	memoryID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	filePath := filepath.Join(cfg.MemoryDir, "memory_"+memoryID+".md")
	if err := os.MkdirAll(cfg.MemoryDir, 0755); err != nil {
		return fmt.Sprintf("Failed to create memory directory: %v", err)
	}

	memory := Memory{
		ID:          memoryID,
		Name:        "Memory " + memoryID,
		Description: text,
		Type:        memoryType,
		Project:     projectName,
		Session:     "default",
		Tags:        tags,
		Created:     time.Now().UTC(),
		Content:     text,
	}

	content := formatMemoryFile(memory)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Sprintf("Failed to write memory file: %v", err)
	}
	updateSearchIndex(cfg)

	out, _ := json.Marshal(map[string]any{
		"id":      memoryID,
		"status":  "remembered",
		"type":    memoryType,
		"project": projectName,
		"tags":    tags,
	})
	return string(out)
}

// mcpList implements the memgraph_list tool.
func mcpList(cfg *Config, args map[string]any) string {
	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	session := mcpGetString(args, "session")
	tags := mcpGetStringSlice(args, "tags")
	limit := mcpGetInt(args, "limit", 50)

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		return "No memories found (directory not initialized)."
	}

	projectFilter := ""
	if !cfg.ScopeResolved {
		projectFilter = projectName
	}

	var memories []Memory
	for _, memory := range index.Memories {
		if projectFilter != "" && memory.Project != projectFilter {
			continue
		}
		if session != "" && memory.Session != session {
			continue
		}
		if !memoryHasAllTags(memory.Tags, tags) {
			continue
		}
		memories = append(memories, memory)
	}
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Created.After(memories[j].Created)
	})
	if limit > 0 && len(memories) > limit {
		memories = memories[:limit]
	}

	out, _ := json.Marshal(map[string]any{
		"count":   len(memories),
		"results": memories,
	})
	return string(out)
}

// mcpEdit implements the memgraph_edit tool.
func mcpEdit(cfg *Config, args map[string]any) string {
	id := mcpGetString(args, "id")
	if id == "" {
		return "Error: id is required"
	}
	newContent := mcpGetString(args, "text")
	if newContent == "" {
		return "Error: text (new content) is required"
	}

	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	memoryFile, ok := findMemoryFileByID(cfg.MemoryDir, id)
	if !ok {
		return fmt.Sprintf("Memory %s not found", id)
	}

	data, err := os.ReadFile(memoryFile)
	if err != nil {
		return fmt.Sprintf("Cannot read memory file: %v", err)
	}

	memory := parseMemory(string(data), filepath.Base(memoryFile))
	description := newContent
	if len(description) > 50 {
		description = description[:50] + "..."
	}
	if idx := strings.Index(description, "\n"); idx != -1 && idx < 50 {
		description = description[:idx]
	}

	memory.Content = newContent
	memory.Description = description
	if t := mcpGetString(args, "type"); t != "" {
		memory.Type = t
	}
	if p := mcpGetString(args, "project"); p != "" {
		memory.Project = p
	}
	if s := mcpGetString(args, "session"); s != "" {
		memory.Session = s
	}
	if tags := mcpGetStringSlice(args, "tags"); tags != nil {
		memory.Tags = tags
	}

	if err := os.WriteFile(memoryFile, []byte(formatMemoryFile(memory)), 0644); err != nil {
		return fmt.Sprintf("Cannot write memory file: %v", err)
	}
	updateSearchIndex(cfg)

	out, _ := json.Marshal(map[string]any{
		"id":          id,
		"status":      "updated",
		"description": description,
	})
	return string(out)
}

// mcpDelete implements the memgraph_delete tool.
func mcpDelete(cfg *Config, args map[string]any) string {
	id := mcpGetString(args, "id")
	if id == "" {
		return "Error: id is required"
	}

	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	memoryFile, ok := findMemoryFileByID(cfg.MemoryDir, id)
	if !ok {
		return fmt.Sprintf("Memory %s not found", id)
	}

	if err := os.Remove(memoryFile); err != nil {
		return fmt.Sprintf("Cannot delete memory file: %v", err)
	}
	updateSearchIndex(cfg)

	out, _ := json.Marshal(map[string]any{
		"id":     id,
		"status": "deleted",
	})
	return string(out)
}

// mcpSessions implements the memgraph_sessions tool.
func mcpSessions(cfg *Config, args map[string]any) string {
	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		return "No memories found (directory not initialized)."
	}

	groups := map[string]*sessionInfo{}
	for _, memory := range index.Memories {
		info := groups[memory.Session]
		if info == nil {
			info = &sessionInfo{Session: memory.Session}
			groups[memory.Session] = info
		}
		info.Count++
		created := memory.Created.Format(time.RFC3339)
		if created > info.LastCreated {
			info.LastCreated = created
		}
	}

	sessions := make([]*sessionInfo, 0, len(groups))
	for _, info := range groups {
		sessions = append(sessions, info)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastCreated > sessions[j].LastCreated
	})

	out, _ := json.Marshal(map[string]any{
		"count":    len(sessions),
		"sessions": sessions,
	})
	return string(out)
}

// mcpImport implements the memgraph_import tool.
func mcpImport(cfg *Config, args map[string]any) string {
	dataStr := mcpGetString(args, "data")
	if dataStr == "" {
		return "Error: data is required"
	}

	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)
	overrideType := mcpGetString(args, "type")

	records, err := parseImportData([]byte(dataStr))
	if err != nil {
		return fmt.Sprintf("Failed to parse import data: %v", err)
	}

	_ = os.MkdirAll(cfg.MemoryDir, 0755)
	imported := 0
	baseTime := time.Now().UnixNano()
	for i, record := range records {
		content := record.Text
		if content == "" {
			content = record.Content
		}
		if content == "" {
			continue
		}

		memoryType := record.Type
		if memoryType == "" {
			memoryType = overrideType
		}
		if memoryType == "" {
			memoryType = cfg.GlobalConfig.DefaultMemoryType
		}

		project := record.Project
		if projectName != "" {
			project = projectName
		}

		session := record.Session
		if session == "" {
			session = "default"
		}

		created := parseCreated(record.Created)
		tags := parseImportTags(record.Tags)

		memoryID := fmt.Sprintf("%d_%d", baseTime, i)
		name := record.Name
		if name == "" {
			name = "Memory " + memoryID
		}
		description := record.Description
		if description == "" {
			description = content
		}

		memory := Memory{
			ID:          memoryID,
			Name:        name,
			Description: description,
			Type:        memoryType,
			Project:     project,
			Session:     session,
			Tags:        tags,
			Created:     created,
			Content:     content,
		}

		outPath := filepath.Join(cfg.MemoryDir, fmt.Sprintf("memory_%s.md", memoryID))
		if err := os.WriteFile(outPath, []byte(formatMemoryFile(memory)), 0644); err == nil {
			imported++
		}
	}
	updateSearchIndex(cfg)

	out, _ := json.Marshal(map[string]any{
		"imported": imported,
		"path":     cfg.MemoryDir,
	})
	return string(out)
}

// mcpInit implements the memgraph_init tool.
func mcpInit(cfg *Config, args map[string]any) string {
	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	if err := os.MkdirAll(cfg.MemoryDir, 0755); err != nil {
		return fmt.Sprintf("Failed to create memory directory: %v", err)
	}

	indexPath := filepath.Join(cfg.MemoryDir, "MEMORY.md")
	indexContent := "# Memory Index\nThis file contains pointers to all project memories.\nLast updated: " + time.Now().Format(time.RFC3339) + "\n\n"
	if err := os.WriteFile(indexPath, []byte(indexContent), 0644); err != nil {
		return fmt.Sprintf("Failed to create index file: %v", err)
	}

	out, _ := json.Marshal(map[string]any{
		"status": "initialized",
		"path":   cfg.MemoryDir,
	})
	return string(out)
}
