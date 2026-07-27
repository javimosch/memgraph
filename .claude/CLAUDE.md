# Memgraph Integration for Claude Code

This file provides project-specific memory for Claude Code via memgraph CLI.

## Memory Loading

To load memories at session start, add this to your Claude Code workflow:

%bash
# Load relevant memories
memgraph recall --json
%

## Adding Memories

%bash
# Add a memory
memgraph remember "Use real database instances in tests, not mocks"

# Add with type, project and tags
memgraph remember --type feedback --project memgraph --tags testing "Use real DBs in tests"
%

## Memory Location

Memories are stored in: /home/jarancibia/.memgraph/projects/-home-jarancibia-ai-sick-memory/memory

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
