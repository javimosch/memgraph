# memgraph 🧠📊

[![CI](https://github.com/javimosch/memgraph/actions/workflows/ci.yml/badge.svg)](https://github.com/javimosch/memgraph/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/javimosch/memgraph?display_name=release)](https://github.com/javimosch/memgraph/releases)
[![license](https://img.shields.io/github/license/javimosch/memgraph)](LICENSE)

**Memgraph CLI** — Knowledge graph and memory system for AI coding agents.

Features a phyllotaxis-spiral visual knowledge graph explorer, automatic skills folder synchronization, TF-IDF + semantic graph linking, and agent-agnostic memory persistence.

> **Status:** v1.4.0 — stable public API. 34 automated tests (graph build, query scoring, ranking order, serve API JSON-error contract, end-to-end ingest, memory write) run with `-race` on every push; CI is green on `master`. The CLI flags, JSON output shapes, and ranking weights are frozen as of v1.4.0.

## Features

- **Galaxy Visualization**: Interactive Via Lactea star map (`memgraph serve`) — spiral arm layout, draggable stars, search, edge toggles.
- **Agent-First CLI**: `memgraph query`, `memgraph related`, `memgraph recommend` — search the graph and get skill recommendations for any task, with JSON output for agent consumption.
- **Copy Visible Paths**: Filter skills (e.g. search "rbm20"), hit `ENTER`, then copy all visible file paths to share directly with your agent.
- **Auto-Sync**: `--sync-dir` monitors any directory and updates the knowledge graph in real-time. Comma-separated multiple directories supported.
- **Watch Mode**: `memgraph watch` monitors skill directories and auto-rebuilds the graph on file changes — no more stale graphs. Polls every 4 seconds by default.
- **Graph-From-Dir**: Ingest any directory of `SKILL.md` or `.md` files into a relational knowledge graph (`references`, `similar`, `shared-keyword`).
- **Planning-with-Files Integration**: Index `task_plan.md`, `findings.md`, and `progress.md` from [planning-with-files](https://github.com/OthmanAdi/planning-with-files) as graph nodes. `memgraph recommend --include-plans` returns relevant past plans alongside skills — cross-session memory for "last time you did this, here's what you tried."
- **Centralized Storage**: All memories stored in `~/.memgraph/` with git-based project scoping.
- **Agent Integration**: Works seamlessly with Claude Code, OpenCode, Copilot, and SuperCLI.

## Installation

### One-line install (Linux x86_64)

```bash
curl -L https://github.com/javimosch/memgraph/releases/latest/download/memgraph -o ~/.local/bin/memgraph && chmod +x ~/.local/bin/memgraph
```

### From Source

```bash
git clone https://github.com/javimosch/memgraph.git
cd memgraph
go build -o memgraph .
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

### 4. Planning-with-Files Integration

[planning-with-files](https://github.com/OthmanAdi/planning-with-files) (26k stars) keeps `task_plan.md`, `findings.md`, and `progress.md` on disk so agent work survives context loss. memgraph can index these files as cross-session memory:

```bash
# Index skills + planning files together
memgraph graph-from-dir --include-plans

# Get recommendations that include past plans
memgraph recommend "fix websocket vulnerability" --include-plans

# List all indexed plans
memgraph plans
```

When you ask "fix websocket vulnerability", memgraph returns not just the `audit-website` skill but also any past `task_plan.md` from a similar task — what you tried, what worked, what failed. This turns planning-with-files from a single-session tool into a cross-session memory.

### 5. Watch Mode

Keep the graph always fresh without manual rebuilds:

```bash
# Watch standard skill directories (auto-discovers ~/.agents/skills, etc.)
memgraph watch

# Watch custom directories with a custom poll interval
memgraph watch --sync-dir ~/my-skills --poll-interval 2

# JSON output for daemon mode
memgraph watch --json
```

## Maintainer

Jarancibia — [@javimosch](https://github.com/javimosch) · [intrane.fr](https://intrane.fr)

Issues and PRs welcome on [GitHub](https://github.com/javimosch/memgraph).

## License

MIT
