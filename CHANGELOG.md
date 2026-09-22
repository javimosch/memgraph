# Changelog

## 1.11.0 — project aliases you can rename, remove, and repair

- Graph explorer UI: the galaxy view is now titled "Memgraph — Galaxy" and
  carries a `galaxy` badge, with Inter as the interface font. `graph.js`
  drops its IIFE wrapper — the file is already an ES module, so the wrapper
  only cost a level of indentation — and folds `init`/`initThree` into
  module top-level. Behaviour is unchanged. The UI is embedded in the
  binary via `go:embed`, so it ships inside this release's asset.
- Fixed `memgraph attach <name> --memory-dir <path>` recording the current
  directory's git remote as the project `remote` instead of the scope the
  memory dir actually belongs to. The scope key is now always derived from
  the memory dir's parent directory name — for
  `~/.memgraph/projects/<scope>/memory` that is `<scope>` — on both the CLI
  and MCP surfaces. The wrong remote made `--project <name>` silently write
  into an unrelated scope.
- Registry integrity warnings now only fire where a mismatch can actually
  misroute a write: a recorded `remote` whose scope dir exists on disk
  (repo-local writes land there while `--project` resolves the registered
  path), or an alias shadowing a same-named scope dir. `remote` is
  metadata — `path` alone drives resolution — so entries created before
  the attach fix or by auto-import, whose remote names no live scope dir,
  are reclassified as `drift`: listed in `memgraph projects` (and its
  `--json` output) but silent everywhere else. Other commands emit at
  most one summary line — `N registry entries need attention; run
  'memgraph projects'` — instead of the full report on every invocation.
- Added `memgraph projects --repair` (and `repair:true` on the MCP
  `memgraph_projects` tool): rewrites drifted `remote` metadata to the
  memory dir's real scope — the same value `attach` records today — in
  one command. Entries whose remote scope dir exists are skipped and
  reported, since both scopes are live and picking one is a data
  decision.
- `attach` still warns at registration time when the new alias collides
  with an existing scope dir.
- Fixed `attach` parsing `os.Args[2:]` directly: a global flag before the
  command (e.g. `memgraph --json attach x`) registered the literal name
  "attach". It now parses the command tail like `detach`/`rename`.
- Added `memgraph detach <name> [--purge]` (alias `unregister`): removes a
  project alias from `~/.memgraph/projects.json` without touching the memory
  files. `--purge` also deletes the registered memory dir — it asks for
  confirmation interactively (skipped under `--json`/`-y`) and refuses any
  path outside `~/.memgraph/projects`, so a corrupted registry entry cannot
  turn it into an arbitrary directory delete.
- Added `memgraph rename <old> <new>`: renames a registered alias in place,
  keeping the memory dir, remote key, and original registration time. Fails
  if the old name is missing or the new one is taken.
- Added `detach` and `rename` to `memgraph_admin` (MCP), the `help-json`
  catalog, and the embedded guide's command groups.

## 1.10.0 — verify, supersede, and an append-only ledger

- Added `memgraph verify`: extracts the paths, URLs, and repositories a memory
  asserts and reports each as fresh, stale, or unknown. Offline and report-only
  by default; `--net` enables URL/repo checks, `--mark` writes `stale:` and
  `verified:` into frontmatter. Exits `90` when anything is stale.
- Staleness requires positive evidence. A missing file counts only when its
  parent directory exists **under the current user's home**; `/etc`, `/usr` and
  friends describe servers, not this machine. Auth walls (401/403) prove a URL
  is alive; timeouts, unreachable hosts, and memories that mention
  `ssh`/`rcx`/`docker exec`/an IP are `unknown`, never stale.
- Added `memgraph supersede <id> (--with <id> | --text <content>) [--reason]`:
  replaces a memory while keeping the belief it recorded. The old memory keeps
  its content, gains `superseded_by`/`superseded_at`/`superseded_reason`, and is
  hidden from `recall` and `list` unless `--include-superseded` is passed.
- Added `memgraph ledger [--since <when>] [--limit <n>]`: the append-only
  `ledger.jsonl` record of every supersede and delete, each carrying a snapshot
  so a memory stays readable after it is gone. `delete` now writes to it too,
  and refuses to delete if the record cannot be written.
- Added MCP surface: `memgraph_supersede` as a core tool, `verify` and `ledger`
  under `memgraph_admin`. `memgraph_delete` now goes through the ledger.
- Fixed a pre-existing round-trip bug: `formatMemoryFile` re-emitted the blank
  line that `parseMemory` kept, so every rewrite grew the body by a newline at
  each end. Harmless at one edit, unbounded under a `verify --mark` cron.

## 1.9.0 — embedded cli-guide-spec discovery

- Added offline `memgraph guide` JSON with the memory/graph model, canonical loop,
  concepts, command groups, examples, and operational gotchas.
- Added `memgraph guide --human` Markdown rendering and `memgraph help-json` catalog.
- Added `GET /guide` and `GET /llms.txt` to the HTTP server using the same embedded guide.
- Added cold-start and HTTP route tests for guide/catalog conformance.
