package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func handleInit(cfg *Config) {
	if err := os.MkdirAll(cfg.MemoryDir, 0755); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to create memory directory: %v", err), false)
		os.Exit(92)
	}

	indexPath := filepath.Join(cfg.MemoryDir, "MEMORY.md")
	indexContent := "# Memory Index\nThis file contains pointers to all project memories.\nLast updated: " + time.Now().Format(time.RFC3339) + "\n\n"
	if err := os.WriteFile(indexPath, []byte(indexContent), 0644); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to create index file: %v", err), false)
		os.Exit(92)
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"status": "initialized",
			"path":   cfg.MemoryDir,
		})
	} else {
		fmt.Printf("Memory system initialized at: %s\n", cfg.MemoryDir)
		fmt.Fprintf(os.Stderr, "Run 'memgraph remember <content>' to add your first memory.\n")
	}
}

func handleStatus(cfg *Config) {
	memoryPath := cfg.MemoryDir
	if _, err := os.Stat(memoryPath); os.IsNotExist(err) {
		if jsonOutput {
			json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"status": "uninitialized"})
		} else {
			fmt.Println("Memory system status: uninitialized")
			fmt.Println("Run 'memgraph init' to initialize.")
		}
		return
	}

	files, err := os.ReadDir(memoryPath)
	if err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to read memory directory: %v", err), false)
		os.Exit(92)
	}

	memoryCount := 0
	for _, file := range files {
		if !file.IsDir() && len(file.Name()) > 7 && file.Name()[:7] == "memory_" && file.Name()[len(file.Name())-3:] == ".md" {
			memoryCount++
		}
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"status": "active",
			"path":   memoryPath,
			"count":  memoryCount,
		})
	} else {
		fmt.Println("Memory system status: active")
		fmt.Printf("Memory directory: %s\n", memoryPath)
		fmt.Printf("Total memories: %d\n", memoryCount)
	}
}

func handleConfig(cfg *Config) {
	globalDir := getGlobalMemgraphDir()
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"global_directory": globalDir,
			"memory_directory": cfg.MemoryDir,
			"project_root":     cfg.ProjectRoot,
			"global_config":    cfg.GlobalConfig,
		})
	} else {
		fmt.Println("Memgraph Configuration:")
		fmt.Println()
		fmt.Printf("Global Directory: %s\n", globalDir)
		fmt.Printf("Memory Directory: %s\n", cfg.MemoryDir)
		if cfg.ProjectRoot != "" {
			fmt.Printf("Project Root: %s\n", cfg.ProjectRoot)
			fmt.Println("Storage Mode: Centralized (git-based scoping)")
		} else {
			fmt.Println("Project Root: Not in a git repository")
			fmt.Println("Storage Mode: Local (fallback)")
		}
		fmt.Println()
		fmt.Println("Global Configuration:")
		fmt.Printf("  Default Memory Type: %s\n", cfg.GlobalConfig.DefaultMemoryType)
		fmt.Printf("  Max Memory Size: %d bytes\n", cfg.GlobalConfig.MaxMemorySize)
		fmt.Printf("  Auto Index: %v\n", cfg.GlobalConfig.AutoIndex)
		fmt.Println()
		fmt.Println("Configuration File:")
		fmt.Printf("  %s\n", filepath.Join(globalDir, "config.json"))
	}
}

func handleBridge(cfg *Config) {
	args, _ := parseCommandArgs(os.Args[2:])
	if len(args) == 0 {
		if jsonOutput {
			errorResponse(85, "invalid_argument", "Usage: memgraph bridge <agent-name>. Available agents: claude-code, opencode, copilot", false)
		} else {
			fmt.Fprintf(os.Stderr, "Usage: memgraph bridge <agent-name>\n")
			fmt.Fprintf(os.Stderr, "Available agents: claude-code, opencode, copilot\n")
		}
		os.Exit(85)
	}

	agent := args[0]
	switch agent {
	case "claude-code":
		generateClaudeCodeBridge(cfg)
	case "opencode":
		generateOpenCodeBridge(cfg)
	case "copilot":
		generateCopilotBridge(cfg)
	default:
		if jsonOutput {
			errorResponse(85, "invalid_argument", fmt.Sprintf("Unknown agent: %s. Available agents: claude-code, opencode, copilot", agent), false)
		} else {
			fmt.Fprintf(os.Stderr, "Unknown agent: %s\n", agent)
			fmt.Fprintf(os.Stderr, "Available agents: claude-code, opencode, copilot\n")
		}
		os.Exit(85)
	}
}

func handleVersion() {
	fmt.Printf("memgraph version %s\n", Version)
}

func handleProfile(cfg *Config) {
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

	total := 0
	byType := map[string]int{}
	byProject := map[string]int{}
	bySession := map[string]int{}
	tagCounts := map[string]int{}
	recent24h := 0
	recent7d := 0

	for _, memory := range index.Memories {
		if opts.Project != "" && memory.Project != opts.Project {
			continue
		}
		if opts.Session != "" && memory.Session != opts.Session {
			continue
		}
		total++
		byType[memory.Type]++
		byProject[memory.Project]++
		bySession[memory.Session]++
		for _, tag := range memory.Tags {
			tagCounts[tag]++
		}
		hours := time.Since(memory.Created).Hours()
		if hours < 24 {
			recent24h++
		}
		if hours < 168 {
			recent7d++
		}
	}

	topTags := topTags(tagCounts, 5)

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"total":        total,
			"by_type":      byType,
			"by_project":   byProject,
			"by_session":   bySession,
			"top_tags":     topTags,
			"recent_24h":   recent24h,
			"recent_7d":    recent7d,
			"storage_path": cfg.MemoryDir,
		})
	} else {
		fmt.Println("Memory Profile")
		fmt.Printf("Storage path: %s\n\n", cfg.MemoryDir)
		fmt.Printf("Total memories: %d\n", total)
		fmt.Println("\nBy type:")
		for _, pair := range sortedMap(byType) {
			fmt.Printf("  %s: %d\n", pair.k, pair.v)
		}
		fmt.Println("\nBy project:")
		for _, pair := range sortedMap(byProject) {
			name := pair.k
			if name == "" {
				name = "(none)"
			}
			fmt.Printf("  %s: %d\n", name, pair.v)
		}
		fmt.Println("\nBy session:")
		for _, pair := range sortedMap(bySession) {
			name := pair.k
			if name == "" {
				name = "(none)"
			}
			fmt.Printf("  %s: %d\n", name, pair.v)
		}
		fmt.Println("\nTop tags:")
		for _, tag := range topTags {
			fmt.Printf("  %s: %d\n", tag.Name, tag.Count)
		}
		fmt.Printf("\nRecent 24h: %d\n", recent24h)
		fmt.Printf("Recent 7d: %d\n", recent7d)
	}
}

type tagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func topTags(counts map[string]int, limit int) []tagCount {
	var tags []tagCount
	for name, count := range counts {
		tags = append(tags, tagCount{Name: name, Count: count})
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Count == tags[j].Count {
			return tags[i].Name < tags[j].Name
		}
		return tags[i].Count > tags[j].Count
	})
	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}
	return tags
}

func sortedMap(m map[string]int) []struct {
	k string
	v int
} {
	var pairs []struct {
		k string
		v int
	}
	for k, v := range m {
		pairs = append(pairs, struct {
			k string
			v int
		}{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
	return pairs
}

func printHelp() {
	fmt.Println("memgraph - Knowledge graph and memory system for AI coding agents")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("    memgraph [COMMAND] [OPTIONS]")
	fmt.Println()
	fmt.Println("COMMANDS:")
	fmt.Println("    init              Initialize memory system for current project")
	fmt.Println("    remember <text>   Add a memory (aliases: save, keep)")
	fmt.Println("    recall [query]    Search/retrieve memories (aliases: search)")
	fmt.Println("    list              List memories (alias: ls)")
	fmt.Println("    sessions          List sessions with memory count and last created")
	fmt.Println("    edit <id> <text>  Edit a memory by ID")
	fmt.Println("    delete <id>       Delete a memory by ID (alias: forget)")
	fmt.Println("    profile           Show memory statistics")
	fmt.Println("    demo              Seed sample demo memories")
	fmt.Println("    import <file>     Import memories from JSON/JSONL (- for stdin)")
	fmt.Println("    graph-from-dir <dir>  Ingest SKILL.md files into a knowledge graph")
	fmt.Println("    query <text>      Search the skill graph (agent-first, use --json)")
	fmt.Println("    related <id|name>  Get skills connected to a given skill")
	fmt.Println("    recommend <task>  Get skill recommendations for a task description")
	fmt.Println("    setup [--sync-dir <dir>]  Configure repo for agent skill discovery (AGENTS.md + bridges + skill)")
	fmt.Println("    serve [--port <n>] [--sync-dir <dir>] [--auto-sync]  Start daemon with embedded graph explorer UI")
	fmt.Println("    watch [--sync-dir <dir>] [--poll-interval <sec>]  Monitor skill dirs and auto-rebuild graph on changes")
	fmt.Println("    plans             List indexed planning-with-files task plans (use --include-plans with graph-from-dir)")
	fmt.Println("    feedback \"<msg>\" [-kind bug|idea|praise|note] [-context \"...\"]  Report feedback (dual-write to app + relay)")
	fmt.Println("    status            Show system status")
	fmt.Println("    config            Show configuration and storage location")
	fmt.Println("    bridge <agent>    Generate agent-specific integration")
	fmt.Println("    --help            Show this help message")
	fmt.Println("    --version         Show version information")
	fmt.Println()
	fmt.Println("OPTIONS:")
	fmt.Println("    save <text> [--text <text>] [--project <name>] [--tags <a,b,c>] [--type <type>] [--session <id>]")
	fmt.Println("    search <query> [--query <query>] [--project <name>] [--session <id>] [--tags <a,b,c>] [--tag-only] [--weights '<json>'] [--limit <n>]")
	fmt.Println("    list [--project <name>] [--session <id>] [--tags <a,b,c>] [--limit <n>]")
	fmt.Println("    profile [--project <name>] [--session <id>]")
	fmt.Println("    --json, -j            Output in JSON format")
	fmt.Println("    --no-interactive, -y  Disable all prompts")
	fmt.Println("    --memory-dir <dir>    Override memory directory")
	fmt.Println("    --type <type>         Memory type (user, feedback, project, reference)")
	fmt.Println("    --project <name>      Project name for remember/recall/list/edit/import/profile")
	fmt.Println("    --session <id>        Session for remember/recall/list/import/profile")
	fmt.Println("    --tags <a,b,c>        Comma-separated tags for remember/recall/list/edit")
	fmt.Println("    --tag-only            Match query terms against tags only (recall/search)")
	fmt.Println("    --weights '<json>'    Override search scoring weights (recall/search)")
	fmt.Println("    --text <text>         Memory content for remember/save")
	fmt.Println("    --query <query>       Search query for recall/search")
	fmt.Println("    --limit <n>           Limit recall/list results")
	fmt.Println("    --port <n>            Port for the serve command (default 8080)")
	fmt.Println()
	fmt.Println("AGENT BRIDGES:")
	fmt.Println("    bridge claude-code    Generate Claude Code integration")
	fmt.Println("    bridge opencode       Generate OpenCode integration")
	fmt.Println("    bridge copilot        Generate Copilot integration")
	fmt.Println()
	fmt.Println("EXIT CODES:")
	fmt.Println("    0        Success")
	fmt.Println("    1        Generic failure")
	fmt.Println("    80-89    Input/validation errors")
	fmt.Println("    90-99    Resource/state errors")
	fmt.Println("    100-109  Integration/external errors")
	fmt.Println("    110-119  Internal software errors")
}
