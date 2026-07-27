# memgraph-cli 🧠📊

**Memgraph CLI** — Knowledge graph and memory system for AI coding agents.

Features a phyllotaxis-spiral visual knowledge graph explorer, automatic skills folder synchronization, TF-IDF + semantic graph linking, and agent-agnostic memory persistence.

## Features

- **Spiral Graph Explorer**: Interactive web UI (`memgraph serve`) with phyllotaxis spiral layout, search, filters, and node inspector.
- **Copy Visible Paths**: Filter skills/memories (e.g. search "rbm20"), hit `ENTER`, then copy all visible file paths to share directly with your agent.
- **Auto-Sync (`--auto-sync`)**: Automatically monitors `~/.agents/skills` (or any custom directory) and updates the knowledge graph in real-time.
- **Graph-From-Dir**: Ingest any directory of `SKILL.md` files into a relational knowledge graph (`references`, `similar`, `shared-keyword`).
- **Centralized Storage**: All memories stored in `~/.memgraph/` with git-based project scoping.
- **Agent Integration**: Works seamlessly with Claude Code, OpenCode, Copilot, and SuperCLI.

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/javimosch/memgraph-cli.git
cd memgraph-cli

# Build the binary
go build -o memgraph .

# Install to path
cp memgraph ~/.local/bin/
```

## Quickstart

### 1. Ingest Skills & Explore Knowledge Graph

```bash
# Ingest all your global agent skills into a graph
memgraph graph-from-dir ~/.agents/skills

# Start the web UI server with auto-sync enabled
memgraph serve --auto-sync --port 8080
```

Open `http://localhost:8080` to explore your agent skills constellation!

### 2. Standard Memory Usage

```bash
# Initialize memory system for current project
memgraph init

# Save a memory
memgraph remember "Use real database instances in tests, not mocks" --type feedback

# Recall memories
memgraph recall "database" --json

# List memories
memgraph list
```

### 3. Agent Bridges

Generate bridge configs for your AI agents:

```bash
memgraph bridge claude-code
memgraph bridge opencode
memgraph bridge copilot
```

## License

MIT
