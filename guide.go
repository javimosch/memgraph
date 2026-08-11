package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// guideData is deliberately built in source, rather than loaded from the
// repository or network, so every released binary can teach itself offline.
func guideData() map[string]interface{} {
	return map[string]interface{}{
		"memgraph":  "a local knowledge graph and memory system for AI coding agents",
		"one_liner": "Memgraph stores durable memories and indexed skill graphs on disk, then exposes ranked recall, recommendations, bridges, and an optional HTTP/MCP server. It is agent-first: JSON output, explicit project scopes, and no hosted dependency.",
		"model": map[string]interface{}{
			"memory":  "Markdown memory files plus a searchable local index; the filesystem remains inspectable and portable.",
			"graph":   "SKILL.md and plan files become nodes connected by references, similarity, and shared keywords.",
			"scoping": "Git-based project scopes live under ~/.memgraph/projects; --memory-dir is the explicit escape hatch.",
			"agents":  "CLI JSON, stdio MCP, HTTP graph search, and generated Claude Code/OpenCode/Copilot bridges are alternate faces of the same store.",
			"storage": "No remote service is required. `init`, `remember`, `recall`, and graph ingestion operate locally.",
			"server":  "`serve` exposes the graph explorer and JSON APIs; `watch` keeps an indexed skill directory fresh.",
		},
		"loop": []string{
			"Run `memgraph guide` to learn the model and `memgraph help-json` to inspect the catalog.",
			"Run `memgraph init` in a project before storing durable memories.",
			"Use `memgraph remember` for facts, decisions, and feedback that should survive sessions.",
			"Use `memgraph graph-from-dir` to index skills and plans, then `query`, `related`, or `recommend` to find relevant context.",
			"Drive integrations with `--json`, MCP, or a generated agent bridge; use `serve` for the visual/API surface.",
		},
		"concepts": map[string]interface{}{
			"memory":        "One durable fact or note, stored as Markdown with metadata and a stable ID.",
			"project_scope": "The repository-aware namespace selected from Git metadata, or explicitly with --project/--memory-dir.",
			"skill_graph":   "An index of skills and plans with searchable nodes and relationship edges.",
			"recommend":     "Task-oriented ranking that combines text relevance with graph connections.",
			"session":       "An optional caller-provided label that groups memories from one agent run.",
			"json":          "Pass --json/-j for structured command data; human output is intended for terminals only.",
			"mcp":           "A stdio JSON-RPC server exposing memory, graph, and administrative tools to agent hosts.",
		},
		"commands": map[string]interface{}{
			"memory": []string{
				"memgraph init",
				"memgraph remember <text> [--type <type>] [--project <name>] [--tags <a,b,c>] [--session <id>]",
				"memgraph recall [query] [--json]",
				"memgraph read <id[/slug]>",
				"memgraph list [--json]",
				"memgraph edit <id> <text>",
				"memgraph delete <id>",
				"memgraph sessions [--json]",
			},
			"graph": []string{
				"memgraph graph-from-dir <dir> [--include-plans]",
				"memgraph query <text> --json",
				"memgraph related <id|name> --json",
				"memgraph recommend <task> --json",
				"memgraph plans --json",
			},
			"integration": []string{
				"memgraph serve [--sync-dir <dir>] [--port <n>]",
				"memgraph watch [--sync-dir <dir>] [--poll-interval <sec>]",
				"memgraph mcp",
				"memgraph bridge claude-code|opencode|copilot",
				"memgraph setup [--sync-dir <dir>]",
			},
			"discovery": []string{
				"memgraph guide [--human]",
				"memgraph help",
				"memgraph help-json",
				"memgraph version",
				"memgraph status",
				"memgraph config",
			},
			"feedback": []string{
				"memgraph feedback <message> [-kind bug|idea|praise|note] [-context <text>]",
			},
		},
		"examples": []map[string]interface{}{
			{
				"goal": "start project memory",
				"do":   []string{"memgraph init", "memgraph remember \"Use real database instances in tests\" --type feedback", "memgraph recall database --json"},
			},
			{
				"goal": "index and query agent skills",
				"do":   []string{"memgraph graph-from-dir ~/.agents/skills --include-plans", "memgraph recommend \"fix websocket vulnerability\" --json", "memgraph related jar-rbm21-manage --json"},
			},
			{
				"goal": "serve a live graph to an agent",
				"do":   []string{"memgraph serve --sync-dir ~/.agents/skills --port 8080", "curl -s http://127.0.0.1:8080/api/search?q=proxmox", "memgraph mcp"},
			},
		},
		"gotchas": []string{
			"`guide` and `help-json` are safe cold-start commands; other commands may require an initialized memory directory or indexed graph.",
			"`--json` changes the output format but does not make a failed command successful; semantic error exits remain actionable.",
			"Project scoping depends on Git metadata when no --memory-dir is supplied; use --project or --memory-dir when operating outside the expected repository.",
			"`serve` exposes the HTTP API on its configured address; put it behind the appropriate network boundary before sharing it beyond localhost or a trusted reverse proxy.",
			"`watch` polls for changes rather than using a kernel filesystem watcher, so updates are periodic and portable rather than instantaneous.",
			"Community/agent-generated memories are data, but they are still operator input: inspect content before treating a recommendation as authoritative.",
		},
		"version":  Version,
		"see_also": []string{"memgraph help-json", "memgraph version", "memgraph status", "https://cli-specs.intrane.fr/"},
	}
}

// commandCatalog is the discoverable command surface. Keep aliases explicit so
// an agent does not have to infer them from the human help text.
func commandCatalog() map[string]interface{} {
	return map[string]interface{}{
		"name":    "memgraph",
		"version": Version,
		"commands": []map[string]interface{}{
			{"name": "init", "usage": "memgraph init", "summary": "initialize the project memory directory"},
			{"name": "remember", "aliases": []string{"save", "keep"}, "usage": "memgraph remember <text> [options]", "summary": "store a durable memory"},
			{"name": "recall", "aliases": []string{"search"}, "usage": "memgraph recall [query] [options]", "summary": "search and retrieve memories"},
			{"name": "read", "usage": "memgraph read <id[/slug]>", "summary": "read a complete memory or section"},
			{"name": "list", "aliases": []string{"ls"}, "usage": "memgraph list [options]", "summary": "list memories"},
			{"name": "sessions", "usage": "memgraph sessions", "summary": "list memory sessions"},
			{"name": "edit", "usage": "memgraph edit <id> <text>", "summary": "edit a memory"},
			{"name": "delete", "aliases": []string{"forget"}, "usage": "memgraph delete <id>", "summary": "delete a memory"},
			{"name": "status", "usage": "memgraph status", "summary": "show memory system status"},
			{"name": "config", "usage": "memgraph config", "summary": "show storage and global configuration"},
			{"name": "profile", "usage": "memgraph profile [options]", "summary": "show memory statistics"},
			{"name": "projects", "usage": "memgraph projects", "summary": "list project scopes"},
			{"name": "attach", "usage": "memgraph attach <name> [options]", "summary": "register a named project scope"},
			{"name": "demo", "usage": "memgraph demo", "summary": "seed sample memories"},
			{"name": "import", "usage": "memgraph import <file|->", "summary": "import JSON or JSONL memories"},
			{"name": "graph-from-dir", "usage": "memgraph graph-from-dir <dir> [options]", "summary": "index skills and plans into the graph"},
			{"name": "query", "usage": "memgraph query <text> [options]", "summary": "search the skill graph"},
			{"name": "related", "usage": "memgraph related <id|name>", "summary": "find connected skills"},
			{"name": "recommend", "usage": "memgraph recommend <task> [options]", "summary": "rank skills and plans for a task"},
			{"name": "plans", "usage": "memgraph plans", "summary": "list indexed planning files"},
			{"name": "setup", "usage": "memgraph setup [options]", "summary": "configure agent skill discovery"},
			{"name": "serve", "usage": "memgraph serve [options]", "summary": "serve the graph explorer and JSON API"},
			{"name": "watch", "usage": "memgraph watch [options]", "summary": "poll skill directories and rebuild the graph"},
			{"name": "bridge", "usage": "memgraph bridge <claude-code|opencode|copilot>", "summary": "generate an agent integration"},
			{"name": "feedback", "usage": "memgraph feedback <message> [options]", "summary": "best-effort dual-write feedback"},
			{"name": "mcp", "usage": "memgraph mcp", "summary": "run the stdio MCP server"},
			{"name": "guide", "usage": "memgraph guide [--human]", "summary": "print the embedded agent guide"},
			{"name": "help-json", "usage": "memgraph help-json", "summary": "print this machine-readable catalog"}, {"name": "version", "usage": "memgraph version", "summary": "print the binary version"},
			{"name": "help", "aliases": []string{"-h", "--help"}, "usage": "memgraph help", "summary": "print the terminal help text"},
		},
	}
}

func guideEnvelope() map[string]interface{} {
	return map[string]interface{}{"version": "1.0", "data": guideData()}
}

func registerGuideRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/guide", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", false)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(guideEnvelope())
	})
	mux.HandleFunc("/llms.txt", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", false)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "memgraph — local knowledge graph and memory for AI coding agents")
		fmt.Fprintln(w, "Run `memgraph guide` or GET /guide for the embedded operator model.")
		fmt.Fprintln(w, "Quick start: memgraph init; memgraph remember \\\"a fact\\\"; memgraph graph-from-dir <dir>; memgraph recommend \\\"a task\\\" --json")
	})
}

func handleGuide(args []string) {
	human := false
	for _, arg := range args {
		if arg == "--human" {
			human = true
			continue
		}
		errorResponse(85, "invalid_argument", "Usage: memgraph guide [--human]", false)
		os.Exit(85)
	}
	if human {
		fmt.Println(guideHuman())
		return
	}
	successResponse(guideData())
}

func handleHelpJSON() {
	successResponse(commandCatalog())
}

func guideHuman() string {
	return fmt.Sprintf(`# memgraph %s — agent guide

Memgraph is a local knowledge graph and memory system for AI coding agents.
It stores inspectable Markdown memories and indexed skill/plan graphs, then
serves ranked recall, recommendations, bridges, HTTP APIs, and MCP.

Canonical loop
1. memgraph guide
2. memgraph init
3. memgraph remember "a durable fact" --type feedback
4. memgraph graph-from-dir ~/.agents/skills --include-plans
5. memgraph recommend "the task" --json
6. memgraph serve --sync-dir ~/.agents/skills --port 8080

Discovery
- memgraph help-json — complete machine-readable catalog
- memgraph status — memory store state
- memgraph config — storage and configuration
- memgraph version — binary version

Important defaults
- --json/-j produces structured command output.
- Git metadata selects project scope unless --project or --memory-dir overrides it.
- serve is an HTTP/API process; watch polls directories every four seconds.
- guide is embedded and works offline; it never fetches documentation.

See also: memgraph help-json, memgraph version, memgraph status
`, Version)
}
