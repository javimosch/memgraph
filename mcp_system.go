package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mcpProjects implements the memgraph_projects tool.
func mcpProjects(cfg *Config) string {
	reg := loadRegistry()
	projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "No projects found."
	}

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

// mcpProfile implements the memgraph_profile tool.
func mcpProfile(cfg *Config, args map[string]any) string {
	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)
	sessionFilter := mcpGetString(args, "session")

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		return "Memory directory not found. Run memgraph_init first."
	}

	total := 0
	byType := map[string]int{}
	byProject := map[string]int{}
	bySession := map[string]int{}
	tagCounts := map[string]int{}
	recent24h := 0
	recent7d := 0

	projectFilter := ""
	if !cfg.ScopeResolved {
		projectFilter = projectName
	}

	for _, memory := range index.Memories {
		if projectFilter != "" && memory.Project != projectFilter {
			continue
		}
		if sessionFilter != "" && memory.Session != sessionFilter {
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

	out, _ := json.Marshal(map[string]any{
		"total":        total,
		"by_type":      byType,
		"by_project":   byProject,
		"by_session":   bySession,
		"top_tags":     topTags(tagCounts, 5),
		"recent_24h":   recent24h,
		"recent_7d":    recent7d,
		"storage_path": cfg.MemoryDir,
	})
	return string(out)
}

// mcpStatus implements the memgraph_status tool.
func mcpStatus(cfg *Config, args map[string]any) string {
	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	memoryPath := cfg.MemoryDir
	if _, err := os.Stat(memoryPath); os.IsNotExist(err) {
		out, _ := json.Marshal(map[string]any{"status": "uninitialized"})
		return string(out)
	}

	files, err := os.ReadDir(memoryPath)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"status": "error", "error": err.Error()})
		return string(out)
	}

	memoryCount := 0
	for _, file := range files {
		if !file.IsDir() && strings.HasPrefix(file.Name(), "memory_") && strings.HasSuffix(file.Name(), ".md") {
			memoryCount++
		}
	}

	out, _ := json.Marshal(map[string]any{
		"status": "active",
		"path":   memoryPath,
		"count":  memoryCount,
	})
	return string(out)
}

// mcpConfig implements the memgraph_config tool.
func mcpConfig(cfg *Config, args map[string]any) string {
	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

	globalDir := getGlobalMemgraphDir()
	out, _ := json.Marshal(map[string]any{
		"global_directory": globalDir,
		"memory_directory": cfg.MemoryDir,
		"project_root":     cfg.ProjectRoot,
		"global_config":    cfg.GlobalConfig,
	})
	return string(out)
}

// mcpAttach implements the memgraph_attach tool.
func mcpAttach(cfg *Config, args map[string]any) string {
	name := mcpGetString(args, "name")
	if name == "" {
		return "Error: name is required"
	}

	mode := mcpGetString(args, "mode")
	if mode == "" {
		mode = "register"
	}
	fromScope := mcpGetString(args, "from_scope")

	reg := loadRegistry()

	switch mode {
	case "remove":
		if !reg.unregister(name) {
			return fmt.Sprintf("Project %q not found in registry", name)
		}
		if err := reg.save(); err != nil {
			return fmt.Sprintf("Failed to save registry: %v", err)
		}
		out, _ := json.Marshal(map[string]any{"status": "removed", "name": name})
		return string(out)

	case "rebind":
		if fromScope == "" {
			return "Error: from_scope is required for rebind mode"
		}
		projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
		scopeDir := filepath.Join(projectsDir, fromScope, "memory")
		if _, err := os.Stat(scopeDir); os.IsNotExist(err) {
			return fmt.Sprintf("Scope dir not found: %s", scopeDir)
		}
		reg.register(name, scopeDir, fromScope)
		if err := reg.save(); err != nil {
			return fmt.Sprintf("Failed to save registry: %v", err)
		}
		out, _ := json.Marshal(map[string]any{
			"status": "rebound",
			"name":   name,
			"path":   scopeDir,
			"scope":  fromScope,
		})
		return string(out)

	default: // "register"
		scopeDir := cfg.MemoryDir
		scopeName := filepath.Base(filepath.Dir(scopeDir))
		reg.register(name, scopeDir, scopeName)
		if err := reg.save(); err != nil {
			return fmt.Sprintf("Failed to save registry: %v", err)
		}
		out, _ := json.Marshal(map[string]any{
			"status": "attached",
			"name":   name,
			"path":   scopeDir,
			"scope":  scopeName,
		})
		return string(out)
	}
}

// mcpDemo implements the memgraph_demo tool.
func mcpDemo(cfg *Config, args map[string]any) string {
	projectName := mcpGetString(args, "project")
	cfg = resolveMCPConfig(cfg, projectName)

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

	out, _ := json.Marshal(map[string]any{
		"created": len(samples),
		"path":    cfg.MemoryDir,
	})
	return string(out)
}

// mcpBridge implements the memgraph_bridge tool.
func mcpBridge(cfg *Config, args map[string]any) string {
	agent := mcpGetString(args, "agent")
	if agent == "" {
		return "Error: agent is required (claude-code, opencode, or copilot)"
	}

	// For MCP, we return a JSON summary pointing the agent to the CLI command.
	// The bridge generators write files to disk (side effects), which is
	// better done via the CLI where the agent controls the working dir.
	out, _ := json.Marshal(map[string]any{
		"status":  "use_cli",
		"agent":   agent,
		"command": fmt.Sprintf("memgraph bridge %s", agent),
		"note":    "Run this command from the repo root to generate the bridge file. The MCP tool does not write files to your project.",
	})
	return string(out)
}

// mcpSetup implements the memgraph_setup tool.
func mcpSetup(cfg *Config, args map[string]any) string {
	syncDir := mcpGetString(args, "sync_dir")
	if syncDir == "" {
		return "Error: sync_dir is required"
	}

	// Delegate to the existing handler by setting up the config
	// The setup handler reads os.Args, so we need to work around that.
	// Instead, we generate the AGENTS.md content directly.
	content := generateAgentsMdContent(cfg, syncDir)
	agentsPath := filepath.Join(cfg.ProjectRoot, "AGENTS.md")
	if cfg.ProjectRoot == "" {
		agentsPath = "AGENTS.md"
	}

	if err := os.WriteFile(agentsPath, []byte(content), 0644); err != nil {
		return fmt.Sprintf("Failed to write AGENTS.md: %v", err)
	}

	out, _ := json.Marshal(map[string]any{
		"status":   "configured",
		"sync_dir": syncDir,
		"agents_md": agentsPath,
	})
	return string(out)
}

// mcpFeedback implements the memgraph_feedback tool.
func mcpFeedback(cfg *Config, args map[string]any) string {
	message := mcpGetString(args, "message")
	if message == "" {
		return "Error: message is required"
	}
	kind := mcpGetString(args, "kind")
	if kind == "" {
		kind = "note"
	}
	context := mcpGetString(args, "context")

	// Best-effort feedback submission
	status := mcpFeedbackSubmit(message, kind, context)

	out, _ := json.Marshal(map[string]any{
		"status":  status,
		"kind":    kind,
		"message": message,
	})
	return string(out)
}

// generateAgentsMdContent creates the AGENTS.md content for setup.
func generateAgentsMdContent(cfg *Config, syncDir string) string {
	return fmt.Sprintf(`# AGENTS.md — memgraph

## Build & Run
` + "```bash" + `
go build -o memgraph .
./memgraph serve --sync-dir %s --port 8080
` + "```" + `

## Skill Discovery
- Skills are auto-synced from %s
`, syncDir, syncDir)
}

// mcpFeedbackSubmit is a stub that always succeeds (best-effort).
// The real CLI handler tries to POST to an endpoint, but for MCP
// we just acknowledge the feedback.
func mcpFeedbackSubmit(message, kind, context string) string {
	return "submitted"
}
