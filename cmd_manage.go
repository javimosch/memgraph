package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func handleList(cfg *Config) {
	_, opts := parseCommandArgs(os.Args[2:])

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		if jsonOutput {
			errorResponse(92, "resource_not_found", "Memory directory not found", false)
		} else {
			fmt.Println("Memory directory not found. Run 'memgraph init' first.")
		}
		os.Exit(92)
	}

	var memories []Memory
	projectFilter := opts.Project
	if cfg.ScopeResolved {
		projectFilter = ""
	}
	for _, memory := range index.Memories {
		if projectFilter != "" && memory.Project != projectFilter {
			continue
		}
		if opts.Session != "" && memory.Session != opts.Session {
			continue
		}
		if !memoryHasAllTags(memory.Tags, opts.Tags) {
			continue
		}
		memories = append(memories, memory)
	}
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Created.After(memories[j].Created)
	})
	if opts.Limit > 0 && len(memories) > opts.Limit {
		memories = memories[:opts.Limit]
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"count":   len(memories),
			"results": memories,
		})
	} else {
		fmt.Printf("Memories in %s:\n", cfg.MemoryDir)
		for _, memory := range memories {
			fmt.Printf("  %s — %s\n", memory.ID, memory.Name)
		}
		fmt.Printf("\nTotal memories: %d\n", len(memories))
	}
}

type sessionInfo struct {
	Session     string `json:"session"`
	Count       int    `json:"count"`
	LastCreated string `json:"last_created"`
}

func handleSessions(cfg *Config) {
	_, _ = parseCommandArgs(os.Args[2:])

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		if jsonOutput {
			errorResponse(92, "resource_not_found", "Memory directory not found", false)
		} else {
			fmt.Println("Memory directory not found. Run 'memgraph init' first.")
		}
		os.Exit(92)
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

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"count":    len(sessions),
			"sessions": sessions,
		})
	} else {
		fmt.Printf("Sessions in %s:\n", cfg.MemoryDir)
		for _, info := range sessions {
			name := info.Session
			if name == "" {
				name = "(none)"
			}
			fmt.Printf("  %s: %d memories, last %s\n", name, info.Count, info.LastCreated)
		}
		fmt.Printf("\nTotal sessions: %d\n", len(sessions))
	}
}

func handleEdit(cfg *Config) {
	positional, opts := parseCommandArgs(os.Args[2:])
	if len(positional) < 2 {
		errorResponse(80, "missing_argument", "Memory ID and new content required for edit. Usage: memgraph edit <id> <new content>", false)
		os.Exit(80)
	}

	memoryID := positional[0]
	newContent := strings.Join(positional[1:], " ")

	memoryFile, ok := findMemoryFileByID(cfg.MemoryDir, memoryID)
	if !ok {
		errorResponse(92, "memory_not_found", fmt.Sprintf("Memory with ID %s not found", memoryID), false)
		os.Exit(92)
	}

	content, err := os.ReadFile(memoryFile)
	if err != nil {
		errorResponse(110, "read_error", fmt.Sprintf("Cannot read memory file: %v", err), false)
		os.Exit(110)
	}

	memory := parseMemory(string(content), filepath.Base(memoryFile))
	description := newContent
	if len(description) > 50 {
		description = description[:50] + "..."
	}
	if idx := strings.Index(description, "\n"); idx != -1 && idx < 50 {
		description = description[:idx]
	}

	memory.Content = newContent
	memory.Description = description
	if opts.MemoryType != "" {
		memory.Type = opts.MemoryType
	}
	if opts.ProjectSet {
		memory.Project = opts.Project
	}
	if opts.SessionSet {
		memory.Session = opts.Session
	}
	if opts.TagsSet {
		memory.Tags = opts.Tags
	}

	if err := os.WriteFile(memoryFile, []byte(formatMemoryFile(memory)), 0644); err != nil {
		errorResponse(110, "write_error", fmt.Sprintf("Cannot write memory file: %v", err), false)
		os.Exit(110)
	}

	updateSearchIndex(cfg)

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"id":          memoryID,
			"status":      "updated",
			"description": description,
		})
	} else {
		fmt.Printf("Memory %s updated successfully\n", memoryID)
	}
}

func handleDelete(cfg *Config) {
	positional, _ := parseCommandArgs(os.Args[2:])
	if len(positional) < 1 {
		errorResponse(80, "missing_argument", "Memory ID required for delete", false)
		os.Exit(80)
	}

	memoryID := positional[0]
	memoryFile, ok := findMemoryFileByID(cfg.MemoryDir, memoryID)
	if !ok {
		errorResponse(92, "memory_not_found", fmt.Sprintf("Memory with ID %s not found", memoryID), false)
		os.Exit(92)
	}

	if err := os.Remove(memoryFile); err != nil {
		errorResponse(110, "delete_error", fmt.Sprintf("Cannot delete memory file: %v", err), false)
		os.Exit(110)
	}

	updateSearchIndex(cfg)

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"id":     memoryID,
			"status": "deleted",
		})
	} else {
		fmt.Printf("Memory %s deleted successfully\n", memoryID)
	}
}

func handleImport(cfg *Config) {
	positional, opts := parseCommandArgs(os.Args[2:])
	if len(positional) < 1 {
		errorResponse(85, "invalid_argument", "Import file path required", false)
		os.Exit(85)
	}

	filePath := positional[0]
	var data []byte
	var err error
	if filePath == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(filePath)
	}
	if err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to read import file: %v", err), false)
		os.Exit(92)
	}

	records, err := parseImportData(data)
	if err != nil {
		errorResponse(85, "invalid_argument", fmt.Sprintf("Failed to parse import data: %v", err), false)
		os.Exit(85)
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
			memoryType = opts.MemoryType
		}
		if memoryType == "" {
			memoryType = cfg.GlobalConfig.DefaultMemoryType
		}

		project := record.Project
		if opts.ProjectSet {
			project = opts.Project
		}

		session := record.Session
		if opts.SessionSet {
			session = opts.Session
		}
		if session == "" {
			session = os.Getenv("MEMGRAPH_SESSION")
		}
		if session == "" {
			session = os.Getenv("SICK_MEMORY_SESSION")
		}
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

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"imported": imported,
			"path":     cfg.MemoryDir,
		})
	} else {
		fmt.Printf("Imported %d memories into %s\n", imported, cfg.MemoryDir)
	}
}

type importRecord struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Text        string          `json:"text"`
	Content     string          `json:"content"`
	Type        string          `json:"type"`
	Project     string          `json:"project"`
	Session     string          `json:"session"`
	Created     string          `json:"created"`
	Tags        json.RawMessage `json:"tags"`
}

func parseImportData(data []byte) ([]importRecord, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty import data")
	}

	if trimmed[0] == '[' {
		var records []importRecord
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, err
		}
		return records, nil
	}

	var records []importRecord
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var record importRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}

func parseImportTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '[' {
		var tags []string
		if err := json.Unmarshal(raw, &tags); err != nil {
			return nil
		}
		return tags
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return parseTagsValue(s)
	}
	return nil
}

func parseCreated(value string) time.Time {
	if value == "" {
		return time.Now().UTC()
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC()
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(n, 0).UTC()
	}
	return time.Now().UTC()
}

func handleDemo(cfg *Config) {
	samples := []struct{ c, t, p, tags string }{
		{"Use real database instances in tests instead of mocks.", "feedback", "demo", "testing,database"},
		{"Project uses Go 1.22 with no external dependencies.", "reference", "demo", "go,deps"},
		{"Keep all source files under 500 lines of code.", "project", "demo", "rules,refactor"},
		{"Import supports JSON arrays and JSONL objects.", "reference", "demo", "import,json"},
	}

	_ = os.MkdirAll(cfg.MemoryDir, 0755)
	baseTime := time.Now().UnixNano()
	for i, sample := range samples {
		memoryID := fmt.Sprintf("%d_%d", baseTime, i)
		memory := Memory{
			ID: memoryID, Name: "Memory " + memoryID, Description: sample.c,
			Type: sample.t, Project: sample.p, Tags: parseTagsValue(sample.tags),
			Created: time.Now().UTC().Add(time.Duration(-i) * time.Second), Content: sample.c,
		}
		outPath := filepath.Join(cfg.MemoryDir, fmt.Sprintf("memory_%s.md", memoryID))
		_ = os.WriteFile(outPath, []byte(formatMemoryFile(memory)), 0644)
	}

	updateSearchIndex(cfg)

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"created": len(samples),
			"path":    cfg.MemoryDir,
		})
	} else {
		fmt.Printf("Created %d demo memories in %s\n", len(samples), cfg.MemoryDir)
	}
}
