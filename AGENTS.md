# AGENTS.md — memgraph-cli

## Build & Run
```bash
go build -o memgraph .
./memgraph serve --sync-dir <path>[,<path2>] --port 8080
```
No default sync directory. The user must pass `--sync-dir`. Multiple comma-separated paths supported.

## Architecture
- **Go backend** embeds `ui/*` via `//go:embed` in `cmd_serve.go`
- **Three.js frontend** (ES modules via importmap from unpkg CDN) renders a 2D galaxy
- Storage at `~/.memgraph` (fallback: `~/.sick-memory` for legacy users)
- Session env: `MEMGRAPH_SESSION` (fallback: `SICK_MEMORY_SESSION`)

## Key Caveats

### Frontend
- **No Cytoscape.js or D3** — those were removed. The sole renderer is Three.js with a 2D orthographic-style camera (z=0 plane, rotation disabled)
- **`ui/graph.js` is an ES module** (`import * as THREE from 'three'`) — must be served with `type="module"` in the script tag
- **OrbitControls** comes from `three/addons/controls/OrbitControls.js` (the `examples/jsm/` path, NOT `examples/js/` which 404s on unpkg for modern Three.js versions)
- **Glow halos are a single `THREE.Points` cloud**, not individual meshes. 293 separate sphere meshes killed performance. The Points cloud uses a radial-gradient canvas texture with additive blending — 1 draw call
- **Label sprites** use cached canvas textures keyed by `fontSize:color:text`. Don't regenerate per frame
- **Label scale updates** are cached and only recalculated when camera distance changes >1%
- **Drag vs click**: 4px movement threshold distinguishes them. `controls.enabled = false` during node drag to prevent pan fighting
- **`closeBtn` must be declared** — it was accidentally removed once and caused a ReferenceError that silently killed the entire IIFE, producing a blank graph with no error visible to the user

### Backend
- **`ingestMultiDir` clears old `memory_*.md` files** before writing new ones. This prevents stale nodes from deleted skills persisting across syncs
- **`scanSkillFiles` deduplicates by ID** across source directories (first occurrence wins)
- **`serverState` uses `sync.RWMutex`** — all graph/nodeMap/index access must go through lock
- **Auto-sync polls every 4 seconds** by checking file modification times. Not a filesystem watcher (portability)
- **`--auto-sync` flag still exists** in `utils.go` but no longer picks a default directory. Sync activates when `--sync-dir` is provided or `auto_sync_dir` is set in config

### Graph Construction
- Edges: `references` (skill mentions another by name), `similar` (TF-IDF), `shared-keyword` (overlapping tags)
- `shared-keyword` and `similar` edges are hidden by default in the UI
- Namespace nodes represent project clusters; skill nodes are members

### Docs
- GitHub Pages serves from `/docs` on `master` branch
- Docs must NOT reference machine-specific paths (no `~/.agents/skills` as a default, no `~/handoffs`)
- Use generic examples like `~/.agents/skills` only as user-supplied `--sync-dir` examples, never as defaults

## Verification
```bash
go build -o memgraph . && ./memgraph serve --sync-dir ~/.agents/skills --port 8080
curl -s http://localhost:8080/api/graph | jq '.nodes | length'
# Should return > 0
```
