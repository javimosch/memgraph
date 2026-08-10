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
// Two tiers: 8 core tools (always advertised, ~800 tokens) + 1 admin meta-tool
// for the 15 rare operations (~200 tokens). Total ~1000 tokens vs ~2500 for flat 23.
func getMCPTools() []mcpToolDef {
	return []mcpToolDef{
		// --- Core tools (agent uses these mid-task) ---
		mcpTool("memgraph_projects",
			"List all project scopes with names, memory counts, and paths. Use this first if you don't know what projects exist.",
			`{"type":"object","properties":{},"required":[]}`),
		mcpTool("memgraph_recall",
			"Search memories by query. Returns compact section index by default — use memgraph_read to fetch full content or a specific section.",
			`{"type":"object","properties":{"query":{"type":"string","description":"Search query (supports phrases, exclusions, prefixes, field filters)"},"project":{"type":"string","description":"Project name (works from any dir via registry)"},"tags":{"type":"array","items":{"type":"string"},"description":"Filter by tags (AND)"},"limit":{"type":"integer","default":10},"format":{"type":"string","enum":["index","full","paths"],"default":"index"}},"required":["query"]}`),
		mcpTool("memgraph_read",
			"Read a full memory or a specific section by slug. Use memgraph_recall first to get the memory ID and section slugs.",
			`{"type":"object","properties":{"id":{"type":"string","description":"Memory ID"},"slug":{"type":"string","description":"Section slug (optional — omit for full memory)"},"project":{"type":"string","description":"Project name (if memory is in a different scope)"}},"required":["id"]}`),
		mcpTool("memgraph_save",
			"Store a new memory. Use [slug] markers to split multi-fact memories into addressable sections.",
			`{"type":"object","properties":{"text":{"type":"string","description":"Memory content (supports [slug] section markers)"},"project":{"type":"string","description":"Project name"},"type":{"type":"string","enum":["user","feedback","project","reference"],"default":"user"},"tags":{"type":"array","items":{"type":"string"},"description":"Tags for categorization"}},"required":["text"]}`),
		mcpTool("memgraph_list",
			"List all memories in a project (browse mode, no search query). Sorted by creation date (newest first).",
			`{"type":"object","properties":{"project":{"type":"string","description":"Project name"},"session":{"type":"string","description":"Filter by session ID"},"tags":{"type":"array","items":{"type":"string"},"description":"Filter by tags (AND)"},"limit":{"type":"integer","default":50}},"required":[]}`),
		mcpTool("memgraph_edit",
			"Edit an existing memory's content. Can also update type, project, session, and tags.",
			`{"type":"object","properties":{"id":{"type":"string","description":"Memory ID to edit"},"text":{"type":"string","description":"New content (supports [slug] section markers)"},"project":{"type":"string","description":"Update project name"},"type":{"type":"string","enum":["user","feedback","project","reference"],"description":"Update memory type"},"tags":{"type":"array","items":{"type":"string"},"description":"Update tags"},"session":{"type":"string","description":"Update session ID"}},"required":["id","text"]}`),
		mcpTool("memgraph_delete",
			"Delete a memory by ID. Cannot be undone.",
			`{"type":"object","properties":{"id":{"type":"string","description":"Memory ID to delete"},"project":{"type":"string","description":"Project name (if memory is in a different scope)"}},"required":["id"]}`),
		mcpTool("memgraph_recommend",
			"Get skill recommendations ranked by relevance to a task description. Returns skills with file_path, score, and related skills.",
			`{"type":"object","properties":{"task":{"type":"string","description":"Task description"},"limit":{"type":"integer","default":5}},"required":["task"]}`),

		// --- Admin meta-tool (covers 15 rare operations) ---
		mcpTool("memgraph_admin",
			"Access less-common memgraph operations. Commands: status, config, profile, sessions, init, demo, import, attach, bridge, setup, feedback, query, related, plans, graph_from_dir. Each takes an 'args' object with command-specific parameters (same as the CLI flags).",
			`{"type":"object","properties":{"command":{"type":"string","enum":["status","config","profile","sessions","init","demo","import","attach","bridge","setup","feedback","query","related","plans","graph_from_dir"],"description":"Admin command to run"},"args":{"type":"object","description":"Command-specific arguments (same params as the CLI flags for that command)","additionalProperties":true}},"required":["command"]}`),
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
	// Core tools
	case "memgraph_projects":
		return mcpToolResultFromText(mcpProjects(cfg))
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
	case "memgraph_recommend":
		return mcpToolResultFromText(mcpRecommend(cfg, args))

	// Admin meta-tool — dispatches to all 15 rare operations
	case "memgraph_admin":
		return mcpAdminDispatch(cfg, args)

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

// mcpAdminDispatch routes memgraph_admin calls to the appropriate handler.
// The 'command' field selects the operation; 'args' holds command-specific params.
func mcpAdminDispatch(cfg *Config, args map[string]any) jsonrpcMessage {
	command := mcpGetString(args, "command")
	if command == "" {
		return mcpError(-32602, "memgraph_admin requires a 'command' field")
	}

	// Extract the nested args object
	subArgs, _ := args["args"].(map[string]any)
	if subArgs == nil {
		subArgs = map[string]any{}
	}

	switch command {
	// Memory — rare ops
	case "sessions":
		return mcpToolResultFromText(mcpSessions(cfg, subArgs))
	case "import":
		return mcpToolResultFromText(mcpImport(cfg, subArgs))
	case "init":
		return mcpToolResultFromText(mcpInit(cfg, subArgs))

	// System / Discovery
	case "status":
		return mcpToolResultFromText(mcpStatus(cfg, subArgs))
	case "config":
		return mcpToolResultFromText(mcpConfig(cfg, subArgs))
	case "profile":
		return mcpToolResultFromText(mcpProfile(cfg, subArgs))
	case "attach":
		return mcpToolResultFromText(mcpAttach(cfg, subArgs))
	case "demo":
		return mcpToolResultFromText(mcpDemo(cfg, subArgs))
	case "bridge":
		return mcpToolResultFromText(mcpBridge(cfg, subArgs))
	case "setup":
		return mcpToolResultFromText(mcpSetup(cfg, subArgs))
	case "feedback":
		return mcpToolResultFromText(mcpFeedback(cfg, subArgs))

	// Skill Graph
	case "query":
		return mcpToolResultFromText(mcpQuery(cfg, subArgs))
	case "related":
		return mcpToolResultFromText(mcpRelated(cfg, subArgs))
	case "plans":
		return mcpToolResultFromText(mcpPlans(cfg))
	case "graph_from_dir":
		return mcpToolResultFromText(mcpGraphFromDir(cfg, subArgs))

	default:
		return mcpError(-32602, fmt.Sprintf("Unknown admin command: %s", command))
	}
}
