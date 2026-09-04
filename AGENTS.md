# AGENTS.md — memgraph

## Agent-first discovery
- `memgraph guide` emits the embedded cli-guide-spec JSON mental model; `--human` renders Markdown.
- `memgraph help-json` emits the complete machine-readable command catalog.
- The HTTP server exposes the same guide at `GET /guide` and a short breadcrumb at `GET /llms.txt`.
- These discovery commands must work offline before config, registry, or memory-store loading.

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

### Verification & Governance
- **`verify` is report-only by default and offline by default.** Writing (`--mark`)
  and the network (`--net`) are both opt-in. Do not flip either default: this runs
  over ~1,900 memories across 43 scopes, where a blip would demote true memories
  en masse.
- **Staleness needs positive evidence.** `verdictStale` is only ever returned for
  a path whose parent exists *under the current user's home*, or a definitive
  404/410. Everything ambiguous is `verdictUnknown`. Widening this was tried and
  reverted: allowing any existing parent produced 75 false positives out of 380
  memories in the `system` scope, because `/etc/systemd/system` exists on every box
  while the memory was describing dk1 or rbm21.
- **`hasRemoteContext` discounts path claims** in any memory mentioning
  ssh/scp/rsync/rcx/remotecmd/pct/docker exec/`user@host`/an IPv4. Those paths live
  on a filesystem this process cannot see.
- **Paths ending in `-` or `_` are dropped** as truncated template fragments
  (`chromium-<version>` cut at the placeholder), not reported as missing files.
- **`formatMemoryFile` must stay idempotent.** It trims the body's surrounding
  newlines because `parseMemory` keeps the blank line after the frontmatter; without
  the trim every rewrite grows the file. `verify --mark` on a cron rewrites the same
  files forever, so this is load-bearing — `TestMarkMemoryRoundTripsThroughFrontmatter`
  guards it.
- **The ledger is written before the mutation, never after.** `supersede` and
  `delete` both refuse to proceed if `appendLedger` fails: an action whose record
  was lost must not appear to have happened.
- **Both CLI and MCP go through `verifyMemoryContent`.** Do not call
  `extractClaims`/`checkAll` directly from a command — the two surfaces drifting
  apart is exactly what that function exists to prevent.

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
