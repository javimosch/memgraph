package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const agentsMDSnippet = `## Memgraph — Skill Discovery

This project uses [memgraph-cli](https://github.com/javimosch/memgraph-cli) for skill and memory discovery.

**Before starting a task**, run these commands to find relevant skills and context:

` + "```bash" + `
# Get skill recommendations for your current task
memgraph recommend "<your task description>" --json

# Search the skill graph by keyword
memgraph query "<keywords>" --json

# Get skills related to a specific skill
memgraph related "<skill-name>" --json
` + "```" + `

The ` + "`--json`" + ` flag outputs structured data. Read the "file_path" field to load the full skill content.
Always check ` + "`memgraph recommend`" + ` first — it ranks skills by relevance and surfaces related skills you might miss by grepping.
`

const devinSkillContent = `---
title: memgraph-usage
description: Use memgraph-cli to discover relevant skills before starting any task. Run 'memgraph recommend' to find skills ranked by relevance to your task.
keywords: [memgraph, skills, discovery, recommend, search, graph, memory]
---

# Memgraph Skill Discovery

## When to use
Before starting any non-trivial task, use ` + "`memgraph recommend`" + ` to find relevant skills.

## Commands

` + "```bash" + `
# Recommend skills for a task (ranked by relevance + graph boost)
memgraph recommend "deploy traefik with SSL" --json

# Search by keyword
memgraph query "proxmox backup" --json

# Get related skills
memgraph related "jar-rbm21-manage" --json
` + "```" + `

## How to use the output
- Each result has a ` + "`file_path`" + ` field — read that file to load the full skill content
- The ` + "`related`" + ` field shows connected skills (references, similar, shared-keyword)
- Use ` + "`--json`" + ` for structured output, or omit for human-readable format

## Why
- Saves tokens: get ranked descriptions + paths instead of reading 10+ skill files
- Surfaces related skills via graph edges that grep would miss
- Skills are auto-synced from ~/.agents/skills/ and other configured directories
`

func handleSetup(cfg *Config) {
	args := os.Args[2:]
	var syncDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--sync-dir":
			if i+1 < len(args) {
				syncDir = args[i+1]
				i++
			}
		case "--json", "-j":
			jsonOutput = true
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot get current directory: %v\n", err)
		os.Exit(92)
	}

	var actions []string

	// 1. Ensure graph exists — ingest from sync-dir if provided, otherwise
	// auto-discover all standard skill directories
	graphDir := filepath.Join(getGlobalMemgraphDir(), "skills-graph")
	var syncDirs []string
	if syncDir != "" {
		// When --sync-dir is provided, ADD it to the auto-discovered dirs
		// rather than replacing them (which would overwrite the global graph
		// with only the specified dir's skills).
		syncDirs = discoverSkillDirs()
		extra := parseSyncDirs(syncDir)
		for _, d := range extra {
			found := false
			for _, existing := range syncDirs {
				if existing == d {
					found = true
					break
				}
			}
			if !found {
				syncDirs = append(syncDirs, d)
			}
		}
	} else {
		syncDirs = discoverSkillDirs()
	}

	if len(syncDirs) > 0 {
		_, _, _, err := ingestMultiDir(syncDirs, graphDir, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: ingestion failed: %v\n", err)
		} else {
			actions = append(actions, fmt.Sprintf("Ingested skills from %d directories into graph", len(syncDirs)))
		}
	}

	// 2. Upsert AGENTS.md
	agentsPath := filepath.Join(cwd, "AGENTS.md")
	if err := upsertFileSection(agentsPath, "## Memgraph — Skill Discovery", agentsMDSnippet); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update AGENTS.md: %v\n", err)
	} else {
		actions = append(actions, "Updated AGENTS.md with memgraph usage instructions")
	}

	// 3. Create .devin/skills/memgraph-usage/SKILL.md
	devinSkillPath := filepath.Join(cwd, ".devin", "skills", "memgraph-usage", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(devinSkillPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create .devin/skills dir: %v\n", err)
	} else {
		if err := os.WriteFile(devinSkillPath, []byte(devinSkillContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write Devin skill: %v\n", err)
		} else {
			actions = append(actions, "Created .devin/skills/memgraph-usage/SKILL.md")
		}
	}

	// 4. Run bridge for detected agent ecosystems
	if _, err := os.Stat(filepath.Join(cwd, ".claude")); err == nil {
		generateClaudeCodeBridge(cfg)
		actions = append(actions, "Updated .claude/CLAUDE.md bridge")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".opencode")); err == nil {
		generateOpenCodeBridge(cfg)
		actions = append(actions, "Updated .opencode bridge")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".copilot")); err == nil {
		generateCopilotBridge(cfg)
		actions = append(actions, "Updated .copilot bridge")
	}

	// 5. Output
	if jsonOutput {
		fmt.Printf(`{"status":"ok","actions":[`, )
		for i, a := range actions {
			if i > 0 {
				fmt.Print(",")
			}
			fmt.Printf("%q", a)
		}
		fmt.Println("]}")
	} else {
		fmt.Println("memgraph setup complete!")
		fmt.Println()
		for _, a := range actions {
			fmt.Printf("  ✓ %s\n", a)
		}
		fmt.Println()
		fmt.Println("Agents in this repo will now discover and use memgraph for skill lookup.")
		fmt.Println("They'll run 'memgraph recommend \"<task>\"' before starting work.")
	}
}

// discoverSkillDirs finds all standard agent skill directories on the system.
func discoverSkillDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".config", "devin", "skills"),
		filepath.Join(home, ".codeium", "windsurf", "skills"),
	}
	var dirs []string
	for _, d := range candidates {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// upsertFileSection inserts or replaces a section in a markdown file.
// The section is identified by its heading (first line of sectionContent).
func upsertFileSection(path, heading, sectionContent string) error {
	heading = strings.TrimSpace(heading)

	var existing string
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}

	// Check if section already exists
	if strings.Contains(existing, heading) {
		// Replace existing section
		lines := strings.Split(existing, "\n")
		var result []string
		skipping := false
		for _, line := range lines {
			if strings.TrimSpace(line) == heading {
				skipping = true
				// Write the new section content
				result = append(result, sectionContent)
				continue
			}
			if skipping {
				// Skip until next ## heading or end of file
				if strings.HasPrefix(strings.TrimSpace(line), "## ") && strings.TrimSpace(line) != heading {
					skipping = false
					result = append(result, line)
				}
				continue
			}
			result = append(result, line)
		}
		return os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644)
	}

	// Append new section
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	content := existing + "\n" + sectionContent + "\n"
	return os.WriteFile(path, []byte(content), 0644)
}
