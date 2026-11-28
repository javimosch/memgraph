package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const agentsMDSnippet = `## Memgraph — Skill Discovery

This project uses [memgraph](https://github.com/javimosch/memgraph) for skill and memory discovery.

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
description: Use memgraph to discover relevant skills before starting any task. Run 'memgraph recommend' to find skills ranked by relevance to your task.
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
	if err := writeBridgeFile(devinSkillPath, devinSkillContent); errors.Is(err, errBridgeExists) {
		actions = append(actions, "Left the existing .devin/skills/memgraph-usage/SKILL.md unchanged")
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write Devin skill: %v\n", err)
	} else {
		actions = append(actions, "Wrote .devin/skills/memgraph-usage/SKILL.md")
	}

	// 4. Run bridge for detected agent ecosystems. A file that is already
	// there stays as it is.
	for _, bridge := range []struct {
		dir   string
		write func(*Config) (string, error)
	}{
		{".claude", writeClaudeCodeBridge},
		{".opencode", writeOpenCodeBridge},
		{".copilot", writeCopilotBridge},
	} {
		if _, err := os.Stat(filepath.Join(cwd, bridge.dir)); err != nil {
			continue
		}
		if path, err := bridge.write(cfg); errors.Is(err, errBridgeExists) {
			actions = append(actions, "Left the existing "+path+" unchanged")
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write %s: %v\n", path, err)
		} else {
			actions = append(actions, "Wrote "+path+" bridge")
		}
	}

	// 5. Output
	if jsonOutput {
		fmt.Printf(`{"status":"ok","actions":[`)
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
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err // never replace a file that could not be read
	}
	existing = string(data)

	// Replace the section if it is there: from its heading to the next heading
	// of level 1 or 2. A "#" line inside a code block is not a heading.
	lines := strings.Split(existing, "\n")
	quoted := false
	for start, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			quoted = !quoted
		}
		if quoted || strings.TrimSpace(line) != heading {
			continue
		}
		end, fenced := start+1, false
		for ; end < len(lines); end++ {
			t := strings.TrimSpace(lines[end])
			if strings.HasPrefix(t, "```") {
				fenced = !fenced
			} else if !fenced && (strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ")) {
				break
			}
		}
		section := strings.TrimRight(sectionContent, "\n")
		result := append(append(append([]string{}, lines[:start]...), section, ""), lines[end:]...)
		return os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644)
	}

	// Append new section
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	content := existing + "\n" + sectionContent + "\n"
	return os.WriteFile(path, []byte(content), 0644)
}
