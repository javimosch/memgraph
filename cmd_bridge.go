package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

var errBridgeExists = errors.New("the file already exists with other content, so it was left unchanged; " +
	"move it aside and run the command again, then copy back what you need")

// writeBridgeFile creates path. These are files an agent's user writes in too,
// so one that already exists with other content is never replaced.
func writeBridgeFile(path, content string) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, fs.ErrExist) {
		old, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(old) != content {
			return errBridgeExists
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	_, err = f.WriteString(content)
	return err
}

// runBridge writes one bridge file for the command line.
func runBridge(cfg *Config, agent, label, hint string, write func(*Config) (string, error)) {
	path, err := write(cfg)
	if err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("%s: %v", path, err), false)
		os.Exit(92)
	}
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"status":      "bridge_created",
			"agent":       agent,
			"config_file": path,
		})
		return
	}
	fmt.Printf("%s bridge created successfully!\n", label)
	fmt.Printf("Configuration file: %s\n", path)
	fmt.Fprintln(os.Stderr, hint)
}

func writeClaudeCodeBridge(cfg *Config) (string, error) {
	claudeMDContent := fmt.Sprintf(`# Memgraph Integration for Claude Code

This file provides project-specific memory for Claude Code via memgraph CLI.

## Skill Discovery (IMPORTANT — run before starting any task)

Before starting any non-trivial task, use memgraph to find relevant skills:

%%bash
# Get skill recommendations for your current task (ranked by relevance)
memgraph recommend "your task description" --json

# Search the skill graph by keyword
memgraph query "keywords" --json

# Get skills related to a specific skill
memgraph related "skill-name" --json
%%

The '--json' flag outputs structured data. Read the "file_path" field to load
the full skill content. Always check 'memgraph recommend' first — it ranks
skills by relevance and surfaces related skills you might miss by grepping.

## Memory Loading

To load memories at session start, add this to your Claude Code workflow:

%%bash
# Load relevant memories
memgraph recall --json
%%

## Adding Memories

%%bash
# Add a memory
memgraph remember "Use real database instances in tests, not mocks"

# Add with type, project and tags
memgraph remember --type feedback --project memgraph --tags testing "Use real DBs in tests"
%%

## Memory Location

Memories are stored in: %s

## Storage Mode

This project uses centralized storage with git-based scoping.
All git worktrees of this repository share the same memory directory.

## Bridge Commands

The following bridge commands are available:
- /mg - Access memgraph functionality
- /mg remember <content> - Add a memory
- /mg recall [query] - Retrieve memories
- /mg recommend <task> - Get skill recommendations
- /mg status - Check memory system status
- /mg config - Show configuration and storage location
`, cfg.MemoryDir)

	return ".claude/CLAUDE.md", writeBridgeFile(".claude/CLAUDE.md", claudeMDContent)
}

func writeOpenCodeBridge(cfg *Config) (string, error) {
	opencodeConfig := fmt.Sprintf(`# Memgraph Integration for OpenCode

## Skill Discovery (IMPORTANT — run before starting any task)

Before starting any non-trivial task, use memgraph to find relevant skills:

%%bash
# Get skill recommendations for your current task (ranked by relevance)
memgraph recommend "your task description" --json

# Search the skill graph by keyword
memgraph query "keywords" --json

# Get skills related to a specific skill
memgraph related "skill-name" --json
%%

The '--json' flag outputs structured data. Read the "file_path" field to load
the full skill content.

## Configuration

Add this to your OpenCode configuration:

%%json
{
  "memory": {
    "enabled": true,
    "command": "memgraph",
    "path": "%s"
  }
}
%%

## Usage

%%bash
# Load memories
memgraph recall --json

# Add memory
memgraph remember "Project-specific context"
%%
`, cfg.MemoryDir)

	return ".opencode/memory.json", writeBridgeFile(".opencode/memory.json", opencodeConfig)
}

func writeCopilotBridge(cfg *Config) (string, error) {
	copilotConfig := fmt.Sprintf(`# Memgraph Integration for GitHub Copilot

## Skill Discovery (IMPORTANT — run before starting any task)

Before starting any non-trivial task, use memgraph to find relevant skills:

%%bash
# Get skill recommendations for your current task (ranked by relevance)
memgraph recommend "your task description" --json

# Search the skill graph by keyword
memgraph query "keywords" --json

# Get skills related to a specific skill
memgraph related "skill-name" --json
%%

The '--json' flag outputs structured data. Read the "file_path" field to load
the full skill content.

## Configuration

Add this to your .copilot/settings.json:

%%json
{
  "memory": {
    "enabled": true,
    "command": "memgraph recall",
    "path": "%s"
  }
}
%%

## Usage

%%bash
# Load memories before starting work
memgraph recall --json

# Add context during work
memgraph remember "Important project context"
%%
`, cfg.MemoryDir)

	return ".copilot/settings.json", writeBridgeFile(".copilot/settings.json", copilotConfig)
}
