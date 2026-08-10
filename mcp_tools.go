package main

import (
	"encoding/json"
	"fmt"
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
// Every CLI command (except daemons: serve, watch, mcp) has a corresponding MCP tool.
func getMCPTools() []mcpToolDef {
	return []mcpToolDef{
		// --- Memory CRUD ---
		mcpTool("memgraph_recall",
			"Search memories by query. Returns a compact section index by default (slug + preview + line range) — use memgraph_read to fetch full content or a specific section. Supports phrases (\"quoted\"), exclusions (-term), prefixes (term*), and field filters (project:name, type:project, tags:a,b).",
			`{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"project":{"type":"string","description":"Project name (resolves via registry, works from any dir)"},"tags":{"type":"array","items":{"type":"string"},"description":"Filter by tags (AND)"},"limit":{"type":"integer","description":"Max results","default":10},"format":{"type":"string","enum":["index","full","paths"],"default":"index","description":"Output format"}},"required":["query"]}`),
		mcpTool("memgraph_read",
			"Read a full memory or a specific section by slug. Use memgraph_recall first to get the memory ID and available section slugs.",
			`{"type":"object","properties":{"id":{"type":"string","description":"Memory ID"},"slug":{"type":"string","description":"Section slug (optional — omit for full memory with section index)"},"project":{"type":"string","description":"Project name (if memory is in a different scope)"}},"required":["id"]}`),
		mcpTool("memgraph_save",
			"Store a new memory. Use [slug] markers to split multi-fact memories into addressable sections — agents can retrieve individual sections instead of dumping the whole memory.",
			`{"type":"object","properties":{"text":{"type":"string","description":"Memory content (supports [slug] section markers)"},"project":{"type":"string","description":"Project name"},"type":{"type":"string","enum":["user","feedback","project","reference"],"default":"user","description":"Memory type"},"tags":{"type":"array","items":{"type":"string"},"description":"Tags for categorization"}},"required":["text"]}`),
		mcpTool("memgraph_list",
			"List all memories in a project (no search query — browse mode). Filter by project, session, or tags. Sorted by creation date (newest first).",
			`{"type":"object","properties":{"project":{"type":"string","description":"Project name"},"session":{"type":"string","description":"Filter by session ID"},"tags":{"type":"array","items":{"type":"string"},"description":"Filter by tags (AND)"},"limit":{"type":"integer","description":"Max results","default":50}},"required":[]}`),
		mcpTool("memgraph_edit",
			"Edit an existing memory's content. Can also update type, project, session, and tags.",
			`{"type":"object","properties":{"id":{"type":"string","description":"Memory ID to edit"},"text":{"type":"string","description":"New content (supports [slug] section markers)"},"project":{"type":"string","description":"Update project name"},"type":{"type":"string","enum":["user","feedback","project","reference"],"description":"Update memory type"},"tags":{"type":"array","items":{"type":"string"},"description":"Update tags"},"session":{"type":"string","description":"Update session ID"}},"required":["id","text"]}`),
		mcpTool("memgraph_delete",
			"Delete a memory by ID. Cannot be undone.",
			`{"type":"object","properties":{"id":{"type":"string","description":"Memory ID to delete"},"project":{"type":"string","description":"Project name (if memory is in a different scope)"}},"required":["id"]}`),
		mcpTool("memgraph_sessions",
			"List all sessions in the current or specified project with memory counts and last activity.",
			`{"type":"object","properties":{"project":{"type":"string","description":"Project name"}},"required":[]}`),
		mcpTool("memgraph_import",
			"Import memories from a JSON array or JSONL string. Each record: {text, type, project, session, tags, created, name, description}.",
			`{"type":"object","properties":{"data":{"type":"string","description":"JSON array or JSONL string of memory records"},"project":{"type":"string","description":"Override project for all imported records"},"type":{"type":"string","enum":["user","feedback","project","reference"],"description":"Override type for all imported records"}},"required":["data"]}`),
		mcpTool("memgraph_init",
			"Initialize the memory directory for the current or specified project. Creates the memory dir and MEMORY.md index file.",
			`{"type":"object","properties":{"project":{"type":"string","description":"Project name to initialize"}},"required":[]}`),

		// --- System / Discovery ---
		mcpTool("memgraph_projects",
			"List all project scopes across all repos with names, memory counts, and paths. Use this first if you don't know what projects exist.",
			`{"type":"object","properties":{},"required":[]}`),
		mcpTool("memgraph_profile",
			"Show statistics for a project: total memories, breakdown by type/project/session, top tags, recent activity (24h/7d).",
			`{"type":"object","properties":{"project":{"type":"string","description":"Project name"},"session":{"type":"string","description":"Filter by session"}},"required":[]}`),
		mcpTool("memgraph_status",
			"Show memory system status: active/uninitialized, memory directory path, total memory count.",
			`{"type":"object","properties":{"project":{"type":"string","description":"Project name"}},"required":[]}`),
		mcpTool("memgraph_config",
			"Show current configuration: global directory, memory directory, project root, search weights, default memory type.",
			`{"type":"object","properties":{"project":{"type":"string","description":"Project name"}},"required":[]}`),
		mcpTool("memgraph_attach",
			"Register or rebind a project name in the project registry. 'register' mode: register current repo under a name. 'rebind' mode: rebind an orphaned scope to a name. 'remove' mode: unregister a name.",
			`{"type":"object","properties":{"name":{"type":"string","description":"Project name to register/rebind/remove"},"mode":{"type":"string","enum":["register","rebind","remove"],"default":"register","description":"Operation mode"},"from_scope":{"type":"string","description":"Scope dir name to rebind (required for 'rebind' mode)"}},"required":["name"]}`),
		mcpTool("memgraph_demo",
			"Seed sample memories into the current or specified project. Useful for testing and demos.",
			`{"type":"object","properties":{"project":{"type":"string","description":"Project name"}},"required":[]}`),
		mcpTool("memgraph_bridge",
			"Generate an agent integration file (CLAUDE.md, opencode config, or copilot config) for the current project.",
			`{"type":"object","properties":{"agent":{"type":"string","enum":["claude-code","opencode","copilot"],"description":"Target agent framework"}},"required":["agent"]}`),
		mcpTool("memgraph_setup",
			"Configure a repo for agent skill discovery: updates AGENTS.md, creates Devin skill, generates bridge files.",
			`{"type":"object","properties":{"sync_dir":{"type":"string","description":"Comma-separated directories to scan for skills"}},"required":[]}`),
		mcpTool("memgraph_feedback",
			"Submit feedback (bug, idea, praise, or note) to the memgraph project. Best-effort delivery.",
			`{"type":"object","properties":{"message":{"type":"string","description":"Feedback message"},"kind":{"type":"string","enum":["bug","idea","praise","note"],"default":"note","description":"Feedback kind"},"context":{"type":"string","description":"Additional context"}},"required":["message"]}`),

		// --- Skill Graph ---
		mcpTool("memgraph_recommend",
			"Get skill recommendations ranked by relevance to a task description. Returns skills with file_path, score, and related skills.",
			`{"type":"object","properties":{"task":{"type":"string","description":"Task description"},"limit":{"type":"integer","description":"Max results","default":5}},"required":["task"]}`),
		mcpTool("memgraph_query",
			"Search the skill graph by keywords. Returns matching skills with id, name, description, project, file_path, score, type, tags, and related skills.",
			`{"type":"object","properties":{"keywords":{"type":"string","description":"Search keywords"},"limit":{"type":"integer","description":"Max results","default":10}},"required":["keywords"]}`),
		mcpTool("memgraph_related",
			"Get skills connected to a given skill by graph edges (references, similar, shared-keyword). Returns the skill and its related skills.",
			`{"type":"object","properties":{"target":{"type":"string","description":"Skill ID or name"},"limit":{"type":"integer","description":"Max results","default":10}},"required":["target"]}`),
		mcpTool("memgraph_plans",
			"List all indexed planning files (task_plan.md, TODO.md, PLAN.md, ROADMAP.md, etc.) from the skill graph.",
			`{"type":"object","properties":{},"required":[]}`),
		mcpTool("memgraph_graph_from_dir",
			"Rebuild the skill graph by ingesting SKILL.md and .md files from specified directories. Writes memory_*.md + graph.json to the skills-graph dir.",
			`{"type":"object","properties":{"sync_dirs":{"type":"array","items":{"type":"string"},"description":"Directories to scan for skills"},"include_plans":{"type":"boolean","default":false,"description":"Also ingest planning files (task_plan.md, TODO.md, etc.)"}},"required":["sync_dirs"]}`),
	}
}

// mcpTool is a helper for constructing tool definitions.
func mcpTool(name, desc, schema string) mcpToolDef {
	return mcpToolDef{Name: name, Description: desc, InputSchema: json.RawMessage(schema)}
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
	// Memory CRUD
	case "memgraph_recall":
		return mcpToolResultFromText(mcpRecall(cfg, args))
	case "memgraph_read":
		return mcpToolResultFromText(mcpRead(cfg, args))
	case "memgraph_save":
		return mcpToolResultFromText(mcpSave(cfg, args))
	case "memgraph_list":
		return mcpToolResultFromText(mcpList(cfg, args))
	case "memgraph_edit":
		return mcpToolResultFromText(mcpEdit(cfg, args))
	case "memgraph_delete":
		return mcpToolResultFromText(mcpDelete(cfg, args))
	case "memgraph_sessions":
		return mcpToolResultFromText(mcpSessions(cfg, args))
	case "memgraph_import":
		return mcpToolResultFromText(mcpImport(cfg, args))
	case "memgraph_init":
		return mcpToolResultFromText(mcpInit(cfg, args))

	// System / Discovery
	case "memgraph_projects":
		return mcpToolResultFromText(mcpProjects(cfg))
	case "memgraph_profile":
		return mcpToolResultFromText(mcpProfile(cfg, args))
	case "memgraph_status":
		return mcpToolResultFromText(mcpStatus(cfg, args))
	case "memgraph_config":
		return mcpToolResultFromText(mcpConfig(cfg, args))
	case "memgraph_attach":
		return mcpToolResultFromText(mcpAttach(cfg, args))
	case "memgraph_demo":
		return mcpToolResultFromText(mcpDemo(cfg, args))
	case "memgraph_bridge":
		return mcpToolResultFromText(mcpBridge(cfg, args))
	case "memgraph_setup":
		return mcpToolResultFromText(mcpSetup(cfg, args))
	case "memgraph_feedback":
		return mcpToolResultFromText(mcpFeedback(cfg, args))

	// Skill Graph
	case "memgraph_recommend":
		return mcpToolResultFromText(mcpRecommend(cfg, args))
	case "memgraph_query":
		return mcpToolResultFromText(mcpQuery(cfg, args))
	case "memgraph_related":
		return mcpToolResultFromText(mcpRelated(cfg, args))
	case "memgraph_plans":
		return mcpToolResultFromText(mcpPlans(cfg))
	case "memgraph_graph_from_dir":
		return mcpToolResultFromText(mcpGraphFromDir(cfg, args))

	default:
		return mcpError(-32601, fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

// --- Shared helpers ---

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

// mcpGetString extracts a string arg, returning "" if missing.
func mcpGetString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// mcpGetInt extracts an int arg with a default, from a float64 (JSON numbers).
func mcpGetInt(args map[string]any, key string, def int) int {
	if v, ok := args[key].(float64); ok && v > 0 {
		return int(v)
	}
	return def
}

// mcpGetStringSlice extracts a []string from a []any arg.
func mcpGetStringSlice(args map[string]any, key string) []string {
	var result []string
	if raw, ok := args[key].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				result = append(result, s)
			}
		}
	}
	return result
}

// mcpGetBool extracts a bool arg with a default.
func mcpGetBool(args map[string]any, key string, def bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return def
}
