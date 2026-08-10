package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mcpToolCallParams is the params for a tools/call request.
type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// handleMCPToolsList returns the list of available tools.
func handleMCPToolsList(msg *jsonrpcMessage) jsonrpcMessage {
	tools := getMCPTools()
	result, _ := json.Marshal(map[string]any{
		"tools": tools,
	})
	return jsonrpcMessage{JSONRPC: "2.0", Result: result}
}

// getMCPTools returns the tool definitions for tools/list.
func getMCPTools() []mcpToolDef {
	return []mcpToolDef{
		{
			Name:        "memgraph_projects",
			Description: "List all project scopes across all repos with names, memory counts, and paths. Use this first if you don't know what projects exist.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
		},
		{
			Name:        "memgraph_recall",
			Description: "Search memories by query. Returns a compact section index by default (slug + preview + line range) — use memgraph_read to fetch full content or a specific section. Supports phrases (\"quoted\"), exclusions (-term), prefixes (term*), and field filters (project:name, type:project, tags:a,b).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"Search query"},
					"project":{"type":"string","description":"Project name (resolves via registry, works from any dir)"},
					"tags":{"type":"array","items":{"type":"string"},"description":"Filter by tags (AND)"},
					"limit":{"type":"integer","description":"Max results","default":10},
					"format":{"type":"string","enum":["index","full","paths"],"default":"index","description":"Output format: index (section previews), full (complete content), paths (file paths + line ranges)"}
				},
				"required":["query"]
			}`),
		},
		{
			Name:        "memgraph_read",
			Description: "Read a full memory or a specific section by slug. Use memgraph_recall first to get the memory ID and available section slugs.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"Memory ID"},
					"slug":{"type":"string","description":"Section slug (optional — omit for full memory with section index)"},
					"project":{"type":"string","description":"Project name (if memory is in a different scope)"}
				},
				"required":["id"]
			}`),
		},
		{
			Name:        "memgraph_save",
			Description: "Store a new memory. Use [slug] markers to split multi-fact memories into addressable sections — agents can retrieve individual sections instead of dumping the whole memory.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"text":{"type":"string","description":"Memory content (supports [slug] section markers)"},
					"project":{"type":"string","description":"Project name"},
					"type":{"type":"string","enum":["user","feedback","project","reference"],"default":"user","description":"Memory type"},
					"tags":{"type":"array","items":{"type":"string"},"description":"Tags for categorization"}
				},
				"required":["text"]
			}`),
		},
		{
			Name:        "memgraph_recommend",
			Description: "Get skill recommendations ranked by relevance to a task description. Returns skills with file_path, score, and related skills.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"task":{"type":"string","description":"Task description"},
					"limit":{"type":"integer","description":"Max results","default":5}
				},
				"required":["task"]
			}`),
		},
	}
}

// handleMCPToolsCall dispatches a tools/call request to the appropriate tool handler.
func handleMCPToolsCall(cfg *Config, msg *jsonrpcMessage) jsonrpcMessage {
	var params mcpToolCallParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcpError(-32602, "Invalid params: "+err.Error())
	}

	var args map[string]any
	if len(params.Arguments) > 0 {
		_ = json.Unmarshal(params.Arguments, &args)
	}

	switch params.Name {
	case "memgraph_projects":
		return mcpToolResultFromText(mcpProjects(cfg))
	case "memgraph_recall":
		return mcpToolResultFromText(mcpRecall(cfg, args))
	case "memgraph_read":
		return mcpToolResultFromText(mcpRead(cfg, args))
	case "memgraph_save":
		return mcpToolResultFromText(mcpSave(cfg, args))
	case "memgraph_recommend":
		return mcpToolResultFromText(mcpRecommend(cfg, args))
	default:
		return mcpError(-32601, fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

// mcpError creates a JSON-RPC error response.
func mcpError(code int, message string) jsonrpcMessage {
	return jsonrpcMessage{
		JSONRPC: "2.0",
		Error: &jsonrpcError{
			Code:    code,
			Message: message,
		},
	}
}

// mcpToolResultFromText wraps a text string in a successful tool result.
func mcpToolResultFromText(text string) jsonrpcMessage {
	result, _ := json.Marshal(mcpToolResult{
		Content: []mcpContentBlock{
			{Type: "text", Text: text},
		},
	})
	return jsonrpcMessage{JSONRPC: "2.0", Result: result}
}

// mcpToolError creates a tool execution error (isError: true, visible to LLM).
func mcpToolError(message string) jsonrpcMessage {
	result, _ := json.Marshal(mcpToolResult{
		Content: []mcpContentBlock{
			{Type: "text", Text: message},
		},
		IsError: true,
	})
	return jsonrpcMessage{JSONRPC: "2.0", Result: result}
}

// resolveMCPConfig resolves the memory dir for MCP tool calls.
// If a project name is provided, resolves via registry. Otherwise uses
// git-based scoping (or the default config).
func resolveMCPConfig(cfg *Config, projectName string) *Config {
	if projectName == "" {
		return cfg
	}
	reg := loadRegistry()
	if dir := resolveProjectName(cfg, reg, projectName); dir != "" {
		return &Config{
			MemoryDir:     dir,
			GlobalConfig:  cfg.GlobalConfig,
			ScopeResolved: true,
		}
	}
	return cfg
}

// mcpProjects implements the memgraph_projects tool.
func mcpProjects(cfg *Config) string {
	reg := loadRegistry()
	projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "No projects found."
	}

	// Build registry scope → name map
	registryScopes := make(map[string]string)
	for name, entry := range reg.Projects {
		dir := filepath.Dir(entry.Path)
		scope := filepath.Base(dir)
		registryScopes[scope] = name
	}

	var lines []string
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		scopeName := entry.Name()
		memDir := filepath.Join(projectsDir, scopeName, "memory")
		memCount := countMemoryFiles(memDir)

		displayName := registryScopes[scopeName]
		if displayName == "" {
			displayName = inferProjectName(scopeName)
		}

		lines = append(lines, fmt.Sprintf("  %-20s  %8d memories  %s", displayName, memCount, memDir))
		count++
	}

	if count == 0 {
		return "No projects found."
	}

	header := fmt.Sprintf("Projects (%d):\n", count)
	return header + strings.Join(lines, "\n") + "\n\nUse --project <name> or memgraph_recall with project param to access any scope."
}

// mcpRecall implements the memgraph_recall tool.
func mcpRecall(cfg *Config, args map[string]any) string {
	query, _ := args["query"].(string)
	if query == "" {
		return "Error: query is required"
	}

	projectName, _ := args["project"].(string)
	cfg = resolveMCPConfig(cfg, projectName)

	format, _ := args["format"].(string)
	if format == "" {
		format = "index"
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	var tags []string
	if tagsRaw, ok := args["tags"].([]any); ok {
		for _, t := range tagsRaw {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		return "No memories found (directory not initialized)."
	}

	searchOpts := SearchOptions{
		Project: "",
		Tags:    tags,
		Weights: cfg.GlobalConfig.SearchWeights,
	}
	// If --project was used for scope resolution, don't double-filter.
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

	// Return JSON for structured consumption
	if format == "full" {
		data, _ := json.Marshal(map[string]any{
			"query":   query,
			"count":   len(results),
			"results": results,
		})
		return string(data)
	}

	// index and paths formats — return compact section index as text
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
	id, _ := args["id"].(string)
	if id == "" {
		return "Error: id is required"
	}

	slug, _ := args["slug"].(string)
	projectName, _ := args["project"].(string)

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
		// Full memory with section index
		data, _ := json.Marshal(map[string]any{
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
		return string(data)
	}

	// Section read
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

	// Extract section content
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

	data2, _ := json.Marshal(map[string]any{
		"id":         memory.ID,
		"slug":       matched.Slug,
		"title":      matched.Title,
		"line_start": matched.LineStart,
		"line_end":   matched.LineEnd,
		"file_path":  filePath,
		"content":    sectionContent,
	})
	return string(data2)
}

// mcpSave implements the memgraph_save tool.
func mcpSave(cfg *Config, args map[string]any) string {
	text, _ := args["text"].(string)
	if text == "" {
		return "Error: text is required"
	}

	projectName, _ := args["project"].(string)
	cfg = resolveMCPConfig(cfg, projectName)

	memoryType, _ := args["type"].(string)
	if memoryType == "" {
		memoryType = cfg.GlobalConfig.DefaultMemoryType
		if memoryType == "" {
			memoryType = "user"
		}
	}

	var tags []string
	if tagsRaw, ok := args["tags"].([]any); ok {
		for _, t := range tagsRaw {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	// Generate memory ID (same as CLI handler)
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

	// Update search index
	updateSearchIndex(cfg)

	result, _ := json.Marshal(map[string]any{
		"id":      memoryID,
		"status":  "remembered",
		"type":    memoryType,
		"project": projectName,
		"tags":    tags,
	})
	return string(result)
}

// mcpRecommend implements the memgraph_recommend tool.
func mcpRecommend(cfg *Config, args map[string]any) string {
	task, _ := args["task"].(string)
	if task == "" {
		return "Error: task is required"
	}

	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	// Load the skill graph
	graph, lookup, err := loadGraphForQuery(cfg)
	if err != nil {
		return "No skill graph found. Run 'memgraph graph-from-dir --sync-dir <path>' to build one."
	}

	// Build related map
	relatedMap := buildRelatedMap(graph)

	// Rank nodes (graph boost on, no plans for MCP)
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
		// Add related skills
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

	data, _ := json.Marshal(map[string]any{
		"task":        task,
		"count":       len(results),
		"recommended": results,
	})
	return string(data)
}
