package main

import (
	"fmt"
	"os"
)

func generateClaudeCodeBridge(cfg *Config) {
	claudeMDContent := fmt.Sprintf(`# Memgraph Integration for Claude Code

This file provides project-specific memory for Claude Code via memgraph CLI.

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
- /mg status - Check memory system status
- /mg config - Show configuration and storage location
`, cfg.MemoryDir)

	claudeMDPath := ".claude/CLAUDE.md"
	if err := os.MkdirAll(".claude", 0755); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to create .claude directory: %v", err), false)
		os.Exit(92)
	}
	if err := os.WriteFile(claudeMDPath, []byte(claudeMDContent), 0644); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to write CLAUDE.md: %v", err), false)
		os.Exit(92)
	}

	if jsonOutput {
		successResponse(map[string]interface{}{
			"status":      "bridge_created",
			"agent":       "claude-code",
			"config_file": claudeMDPath,
		})
	} else {
		fmt.Println("Claude Code bridge created successfully!")
		fmt.Printf("Configuration file: %s\n", claudeMDPath)
		fmt.Fprintf(os.Stderr, "Add the bridge commands to your Claude Code workflow.\n")
	}
}

func generateOpenCodeBridge(cfg *Config) {
	opencodeConfig := fmt.Sprintf(`# Memgraph Integration for OpenCode

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

	configPath := ".opencode/memory.json"
	if err := os.MkdirAll(".opencode", 0755); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to create .opencode directory: %v", err), false)
		os.Exit(92)
	}
	if err := os.WriteFile(configPath, []byte(opencodeConfig), 0644); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to write config: %v", err), false)
		os.Exit(92)
	}

	if jsonOutput {
		successResponse(map[string]interface{}{
			"status":      "bridge_created",
			"agent":       "opencode",
			"config_file": configPath,
		})
	} else {
		fmt.Println("OpenCode bridge created successfully!")
		fmt.Printf("Configuration file: %s\n", configPath)
		fmt.Fprintf(os.Stderr, "Add the configuration to your OpenCode setup.\n")
	}
}

func generateCopilotBridge(cfg *Config) {
	copilotConfig := fmt.Sprintf(`# Memgraph Integration for GitHub Copilot

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

	configPath := ".copilot/settings.json"
	if err := os.MkdirAll(".copilot", 0755); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to create .copilot directory: %v", err), false)
		os.Exit(92)
	}
	if err := os.WriteFile(configPath, []byte(copilotConfig), 0644); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to write config: %v", err), false)
		os.Exit(92)
	}

	if jsonOutput {
		successResponse(map[string]interface{}{
			"status":      "bridge_created",
			"agent":       "copilot",
			"config_file": configPath,
		})
	} else {
		fmt.Println("Copilot bridge created successfully!")
		fmt.Printf("Configuration file: %s\n", configPath)
		fmt.Fprintf(os.Stderr, "Add the configuration to your Copilot settings.\n")
	}
}
